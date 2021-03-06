package client

import (
	"bufio"
	// "encoding/json"
	"fmt"
	"github.com/trendrr/goshire/cheshire"
	// "github.com/trendrr/goshire/dynmap"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// A single connection to a cheshire server.
// The connection is considered fail fast
// should be disconnected and reaped

// This is not threadsafe and should only be used in one routine at a time.
type cheshireConn struct {
	net.Conn
	addr         string
	connected    int32
	readTimeout  time.Duration
	writeTimeout time.Duration
	incomingChan chan *cheshire.Response
	outgoingChan chan *cheshireRequest
	exitChan     chan int
	//every new request will push a bool into this chan,
	//it will block once full
	inflightChan chan bool

	//map of txnId to request
	requests     map[string]*cheshireRequest
	requestsLock sync.RWMutex

	connectedAt time.Time
	maxInFlight int
	protocol cheshire.Protocol
}

//wrap a request so we dont lose track of the result channels
type cheshireRequest struct {
	req        *cheshire.Request
	resultChan chan *cheshire.Response
	errorChan  chan error
}

func newCheshireConn(protocol cheshire.Protocol, addr string, writeTimeout time.Duration, maxInFlight int) (*cheshireConn, error) {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return nil, err
	}

	err = protocol.WriteHello(conn)
	if err != nil {
		return nil, err
	}

	nc := &cheshireConn{
		Conn:         conn,
		connected:    1,
		addr:         addr,
		writeTimeout: writeTimeout,
		exitChan:     make(chan int),
		incomingChan: make(chan *cheshire.Response, 25),
		outgoingChan: make(chan *cheshireRequest, 25),

		inflightChan:  make(chan bool, maxInFlight),
		requests:    make(map[string]*cheshireRequest),
		connectedAt: time.Now(),
		protocol: protocol,
		maxInFlight : maxInFlight,
	}
	return nc, nil
}

func (this *cheshireConn) setConnected(v bool) {
	if v {
		atomic.StoreInt32(&this.connected, 1)
	} else {
		atomic.StoreInt32(&this.connected, 0)
	}
}

func (this *cheshireConn) Connected() bool {
	return this.connected == 1
}

//returns the current # of requests in flight
//unsafe
func (this *cheshireConn) inflight() int {
	this.requestsLock.RLock()
	defer this.requestsLock.RUnlock()

	return len(this.requests)
}

// Sends a new request.
// this will check the max inflight, and will block for max 20 seconds waiting for the # inflilght to go down.
// if inflight does not go down it will close the connection and return error.
// A returned error can be considered a disconnect
func (this *cheshireConn) sendRequest(request *cheshire.Request, resultChan chan *cheshire.Response, errorChan chan error) (*cheshireRequest, error) {
	
	select {
	case this.inflightChan <- true :
	case <- time.After(1 * time.Second):
		//should close this connection..
		this.Close()
		return nil, fmt.Errorf("Max inflight sustained for more then 20 seconds, fail")
	}

	if !this.Connected() {
		return nil, fmt.Errorf("Not connected")
	}

	req := &cheshireRequest{
		req:        request,
		resultChan: resultChan,
		errorChan:  errorChan,
	}
	this.outgoingChan <- req
	return req, nil
}

func (this *cheshireConn) Close() {
	if !this.Connected() {
		return //do nothing.
	}
	this.exitChan <- 1
}

func (this *cheshireConn) String() string {
	return this.addr
}

// loop that listens for incoming messages.
func (this *cheshireConn) listener() {
	decoder := this.protocol.NewDecoder(bufio.NewReader(this.Conn))
	log.Printf("Starting Cheshire Connection %s", this.addr)
	defer func() { this.exitChan <- 1 }()
	for {
		res, err := decoder.DecodeResponse()
		if err == io.EOF {
			log.Print(err)
			break
		} else if err != nil {
			log.Print(err)
			break
		}
		this.incomingChan <- res
	}
}

func (this *cheshireConn) cleanup() {
	this.Conn.Close()
	log.Printf("Closing Cheshire Connection: %s", this.addr)

	err := fmt.Errorf("Connection is closed %s", this.addr)
	//now error out all waiting
	for len(this.outgoingChan) > 0 {
		req := <-this.outgoingChan
		//send an error to the error chan
		req.errorChan <- err
	}
	log.Println("ended outchan")
	this.requestsLock.Lock()
	defer this.requestsLock.Unlock()

	for k, v := range this.requests {
		v.errorChan <- err
		delete(this.requests, k)
	}
}

func (this *cheshireConn) eventLoop() {
	go this.listener()

	// writer := bufio.NewWriter(this.Conn)

	defer this.cleanup()
	for this.Connected() {
		select {
		case response := <-this.incomingChan:
			this.requestsLock.RLock()
			req, ok := this.requests[response.TxnId()]
			this.requestsLock.RUnlock()
			if !ok {
				log.Printf("Uhh, received response, but had no request %s", response)
				// for k,_ := range(this.requests) {
				//     log.Println(k)
				// }
				continue //break?
			}
			req.resultChan <- response
			//remove if txn is finished..
			if response.TxnStatus() == "completed" {
				this.requestsLock.Lock()
				delete(this.requests, response.TxnId())
				this.requestsLock.Unlock()
				//pull one from inflight
				<- this.inflightChan
			}
		case request := <-this.outgoingChan:
			//add to the request map
			this.requestsLock.Lock()
			this.requests[request.req.TxnId()] = request
			this.requestsLock.Unlock()

			//send the request
			this.SetWriteDeadline(time.Now().Add(this.writeTimeout))

			_, err := this.protocol.WriteRequest(request.req, this.Conn)
			if err != nil {
				//TODO: uhh, do something..
				log.Print(err)
				continue
			}
			// writer.Flush()

		case <-this.exitChan:
			this.setConnected(false)
		}
	}
}
