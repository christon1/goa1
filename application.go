package goa

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/net/context"

	"github.com/julienschmidt/httprouter"
	log "gopkg.in/inconshreveable/log15.v2"
)

type (
	// Application represents a goa application. At the basic level an application consists of
	// a set of controllers, each implementing a given resource actions. goagen generates
	// global functions - one per resource - that make it possible to mount the corresponding
	// controller onto an application. An application contains the middleware, logger and error
	// handler shared by all its controllers. Setting up an application might look like:
	//
	//	api := goa.New("my api")
	//	api.Use(SomeMiddleware())
	//	api.Use(SomeOtherMiddleware())
	//	rc := NewResourceController()
	//	app.MountResourceController(api, rc)
	//	api.Run(":80")
	//
	// where NewResourceController returns an object that implements the resource actions as
	// defined by the corresponding interface generated by goagen.
	Application struct {
		log.Logger                      // Application logger
		Name         string             // Application name
		ErrorHandler ErrorHandler       // Application error handler
		Middleware   []Middleware       // Middleware chain
		Router       *httprouter.Router // Application router
	}

	// Handler defines the controller handler signatures.
	// Controller handlers accept a context and return an error.
	// The context provides typed access to the request and response state. It implements
	// the golang.org/x/net/context package Context interface so that handlers may define
	// deadlines and cancelation signals - see the Timeout middleware as an example.
	// If a controller handler returns an error then the application error handler is invoked
	// with the request context and the error. The error handler is responsible for writing the
	// HTTP response. See DefaultErrorHandler and TerseErrorHandler.
	Handler func(*Context) error

	// ErrorHandler defines the application error handler signature. The default error handler
	// is DefaultErrorHandler. Call SetErrorHandler to provide a custom error handler. See
	// TerseErrorHandler as an alternative error handler.
	ErrorHandler func(*Context, error)
)

var (
	// Log is the global logger from which other loggers (e.g. request specific loggers) are
	// derived. Configure it by setting its handler prior to calling New.
	// See https://godoc.org/github.com/inconshreveable/log15
	Log log.Logger
)

// Log to STDOUT by default
func init() {
	Log = log.New()
	Log.SetHandler(log.StdoutHandler)
}

// New instantiates an application with the given name.
func New(name string) *Application {
	return &Application{
		Logger:       Log.New("app", name),
		Name:         name,
		ErrorHandler: DefaultErrorHandler,
		Router:       httprouter.New(),
	}
}

// Use adds a middleware to the application middleware chain.
// See NewMiddleware for the list of possible types for middleware.
// goa comes with a set of commonly used middleware, see middleware.go.
func (app *Application) Use(middleware interface{}) {
	m, err := NewMiddleware(middleware)
	if err != nil {
		Fatal("invalid middleware", "middleware", middleware, "err", err)
	}
	app.Middleware = append(app.Middleware, m)
}

// SetErrorHandler defines an application wide error handler.
// The default error handler returns a 500 status code with the error message in the response body.
// TerseErrorHandler provides an alternative implementation that does not send the error message
// in the response body for internal errors (e.g. for production).
// Set it with SetErrorHandler(TerseErrorHandler).
func (app *Application) SetErrorHandler(handler ErrorHandler) {
	app.ErrorHandler = handler
}

// Run starts the HTTP server and sets up a listener on the given host/port.
// It logs an error and exits the process with status 1 if the server fails to start (e.g. if the
// listen port is busy).
func (app *Application) Run(addr string) {
	app.Info("listen", "addr", addr)
	if err := http.ListenAndServe(addr, app.Router); err != nil {
		Fatal("startup failed", "err", err)
	}
}

// NewHTTPRouterHandle returns a httprouter handle from a goa handler. This handle initializes the
// request context by loading the request state, invokes the handler and in case of error invokes
// the application error handler.
// This function is intended for the controller generated code. User code should not have to call
// it directly.
func (app *Application) NewHTTPRouterHandle(resName, actName string, h Handler) httprouter.Handle {
	// Setup middleware outside of closure
	chain := app.Middleware
	ml := len(chain)
	middleware := func(ctx *Context) error {
		if err := h(ctx); err != nil {
			app.ErrorHandler(ctx, err)
		}
		return nil
	}
	for i := range chain {
		middleware = chain[ml-i-1](middleware)
	}
	logger := app.Logger.New("ctrl", resName, "action", actName)
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		// Collect URL and query string parameters
		params := make(map[string]string, len(p))
		for _, param := range p {
			params[param.Key] = param.Value
		}
		q := r.URL.Query()
		query := make(map[string][]string, len(q))
		for name, value := range q {
			query[name] = value
		}

		// Load body if any
		var payload interface{}
		var err error
		if r.ContentLength > 0 {
			decoder := json.NewDecoder(r.Body)
			err = decoder.Decode(&payload)
		}

		// Build context
		gctx, cancel := context.WithCancel(context.Background())
		defer cancel() // Signal completion of request to any child goroutine
		ctx := NewContext(gctx, r, w, params, query, payload)
		ctx.Logger = logger

		// Handle invalid payload
		handler := middleware
		if err != nil {
			handler = func(ctx *Context) error {
				ctx.Respond(400, []byte(fmt.Sprintf(`{"kind":"invalid request","msg":"invalid JSON: %s"}`, err)))
				return nil
			}
			for i := range chain {
				handler = chain[ml-i-1](handler)
			}
		}

		// Invoke middleware chain
		handler(ctx)

		// Make sure a response is sent back to client.
		if !ctx.ResponseWritten() {
			app.ErrorHandler(ctx, fmt.Errorf("unhandled request"))
		}
	}
}

// DefaultErrorHandler returns a 400 response for request validation errors (instances of
// BadRequestError) and a 500 response for other errors. It writes the error message to the
// response body in both cases.
func DefaultErrorHandler(c *Context, e error) {
	status := 500
	if _, ok := e.(*BadRequestError); ok {
		c.Header().Set("Content-Type", "application/json")
		status = 400
	}
	if err := c.Respond(status, []byte(e.Error())); err != nil {
		Log.Error("failed to send default error handler response", "err", err)
	}
}

// TerseErrorHandler behaves like DefaultErrorHandler except that it does not set the response
// body for internal errors.
func TerseErrorHandler(c *Context, e error) {
	status := 500
	var body []byte
	if _, ok := e.(*BadRequestError); ok {
		c.Header().Set("Content-Type", "application/json")
		status = 400
		body = []byte(e.Error())
	}
	if err := c.Respond(status, body); err != nil {
		Log.Error("failed to send terse error handler response", "err", err)
	}
}

// Fatal logs a critical message and exits the process with status code 1.
// This function is meant to be used by initialization code to prevent the application from even
// starting up when something is obviously wrong.
// In particular this function should probably not be used when serving requests.
func Fatal(msg string, ctx ...interface{}) {
	log.Crit(msg, ctx...)
	os.Exit(1)
}
