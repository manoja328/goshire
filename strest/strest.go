package strest

import (
    // "log"
)

// what Strest protocol version we are using.
const StrestVersion = float32(2)

// Standard STREST request.
// See protocol spec https://github.com/trendrr/strest-server/wiki/STREST-Protocol-Spec
type Request struct {
    Strest struct {
        Version float32 `json:"v"`
        Method string `json:"method"`
        Uri string `json:"uri"`
        Params map[string]interface{} `json:"params"`
        Txn struct {
            Id string `json:"id"`
            Accept string `json:"accept"`
        } `json:"txn"`
    } `json:"strest"`
}


// Standard STREST response
// See protocol spec https://github.com/trendrr/strest-server/wiki/STREST-Protocol-Spec
type Response struct {
    DynMap
}

func (this *Response) TxnId() string {
    return this.GetStringOrDefault("strest.txn.id", "")
}

func (this *Response) SetTxnId(id string) {
    this.PutWithDot("strest.txn.id", id)
}

func (this *Response) TxnStatus() string {
    return this.GetStringOrDefault("strest.txn.status", "")
}

func (this *Response) SetTxnStatus(status string) {
    this.PutWithDot("strest.txn.status", status)
}

func (this *Response) StatusCode() int {
    return 200 //this.getIntOrDefault("status.code", 200)
}

func (this *Response) SetStatusCode(code int) {
    this.PutWithDot("status.code", code)
}

func (this *Response) StatusMessage() string {
    return this.GetStringOrDefault("status.message", "")
}

func (this *Response) SetStatusMessage(message string) {
    this.PutWithDot("status.message", message)
}


// Create a new response object.
// Values are all set to defaults
func NewResponse(request *Request) *Response {
    response := &Response{*NewDynMap()}
    response.SetStatusMessage("OK")
    response.SetStatusCode(200)
    response.SetTxnStatus("completed")
    response.SetTxnId(request.Strest.Txn.Id)
    response.PutWithDot("strest.version", StrestVersion)
    return response
}

type Connection interface {
    //writes the response to the underlying channel 
    // i.e. either to an http response writer or json socket.
    Write(*Response) (int, error) 
}

type RouteMatcher interface {
    Match(string) (Controller)
    Register(Controller)
}
type ServerConfig struct {
    *DynMap
    Router RouteMatcher
}

// Creates a new server config with a default routematcher
func NewServerConfig() *ServerConfig {
    return &ServerConfig{NewDynMap(), NewDefaultRouter()}
}

// Registers a controller with the RouteMatcher.  
// shortcut to conf.Router.Register(controller)
func (this *ServerConfig) Register(controller Controller) {
    this.Router.Register(controller)
}

// Configuration for a specific controller.
type Config struct {
    Route string
}

func NewConfig(route string) *Config {
    return &Config{Route : route}
}

// a Controller object
type Controller interface {
    Config() (*Config)
    HandleRequest(*Request, Connection)
}

type DefaultController struct {
    Handlers map[string] func(*Request, Connection)
    Conf *Config
}
func (this *DefaultController) Config() (*Config) {
    return this.Conf
}
func (this *DefaultController) HandleRequest(request *Request, conn Connection) {
    handler := this.Handlers[request.Strest.Method]
    if handler == nil {
        handler = this.Handlers["ALL"]
    }
    if handler == nil {
        //not found!
        //TODO: method not allowed 
        return
    }
    handler(request, conn)
}

// creates a new controller for the specified route for a specific method types (GET, POST, PUT, ect)
func NewController(route string, methods []string, handler func(*Request,Connection)) *DefaultController {
    // def := new(DefaultController)
    // def.Conf = NewConfig(route)

    def := &DefaultController{Handlers : make(map[string] func(*Request, Connection)), Conf : NewConfig(route)}
    for _,m := range methods {
        def.Handlers[m] = handler
    }
    return def
}

// creates a new controller that will process all method types
func NewControllerAll(route string, handler func(*Request,Connection)) *DefaultController {
    return NewController(route, []string{"ALL"}, handler)
}



