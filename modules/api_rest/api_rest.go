package api_rest

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/bettercap/bettercap/session"
	"github.com/bettercap/bettercap/tls"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/evilsocket/islazy/fs"
)

type RestAPI struct {
	session.SessionModule
	server       *http.Server
	username     string
	password     string
	certFile     string
	keyFile      string
	allowOrigin  string
	useWebsocket bool
	upgrader     websocket.Upgrader
	quit         chan bool
}

func NewRestAPI(s *session.Session) *RestAPI {
	mod := &RestAPI{
		SessionModule: session.NewSessionModule("api.rest", s),
		server:        &http.Server{},
		quit:          make(chan bool),
		useWebsocket:  false,
		allowOrigin:   "*",
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}

	mod.AddParam(session.NewStringParameter("api.rest.address",
		session.ParamIfaceAddress,
		session.IPv4Validator,
		"Address to bind the API REST server to."))

	mod.AddParam(session.NewIntParameter("api.rest.port",
		"8081",
		"Port to bind the API REST server to."))

	mod.AddParam(session.NewStringParameter("api.rest.alloworigin",
		mod.allowOrigin,
		"",
		"Value of the Access-Control-Allow-Origin header of the API server."))

	mod.AddParam(session.NewStringParameter("api.rest.username",
		"",
		"",
		"API authentication username."))

	mod.AddParam(session.NewStringParameter("api.rest.password",
		"",
		"",
		"API authentication password."))

	mod.AddParam(session.NewStringParameter("api.rest.certificate",
		"",
		"",
		"API TLS certificate."))

	tls.CertConfigToModule("api.rest", &mod.SessionModule, tls.DefaultLegitConfig)

	mod.AddParam(session.NewStringParameter("api.rest.key",
		"",
		"",
		"API TLS key"))

	mod.AddParam(session.NewBoolParameter("api.rest.websocket",
		"false",
		"If true the /api/events route will be available as a websocket endpoint instead of HTTPS."))

	mod.AddHandler(session.NewModuleHandler("api.rest on", "",
		"Start REST API server.",
		func(args []string) error {
			return mod.Start()
		}))

	mod.AddHandler(session.NewModuleHandler("api.rest off", "",
		"Stop REST API server.",
		func(args []string) error {
			return mod.Stop()
		}))

	return mod
}

type JSSessionRequest struct {
	Command string `json:"cmd"`
}

type JSSessionResponse struct {
	Error string `json:"error"`
}

func (mod *RestAPI) Name() string {
	return "api.rest"
}

func (mod *RestAPI) Description() string {
	return "Expose a RESTful API."
}

func (mod *RestAPI) Author() string {
	return "Simone Margaritelli <evilsocket@gmail.com>"
}

func (mod *RestAPI) isTLS() bool {
	return mod.certFile != "" && mod.keyFile != ""
}

func (mod *RestAPI) Configure() error {
	var err error
	var ip string
	var port int

	if mod.Running() {
		return session.ErrAlreadyStarted
	} else if err, ip = mod.StringParam("api.rest.address"); err != nil {
		return err
	} else if err, port = mod.IntParam("api.rest.port"); err != nil {
		return err
	} else if err, mod.allowOrigin = mod.StringParam("api.rest.alloworigin"); err != nil {
		return err
	} else if err, mod.certFile = mod.StringParam("api.rest.certificate"); err != nil {
		return err
	} else if mod.certFile, err = fs.Expand(mod.certFile); err != nil {
		return err
	} else if err, mod.keyFile = mod.StringParam("api.rest.key"); err != nil {
		return err
	} else if mod.keyFile, err = fs.Expand(mod.keyFile); err != nil {
		return err
	} else if err, mod.username = mod.StringParam("api.rest.username"); err != nil {
		return err
	} else if err, mod.password = mod.StringParam("api.rest.password"); err != nil {
		return err
	} else if err, mod.useWebsocket = mod.BoolParam("api.rest.websocket"); err != nil {
		return err
	}

	if mod.isTLS() {
		if !fs.Exists(mod.certFile) || !fs.Exists(mod.keyFile) {
			err, cfg := tls.CertConfigFromModule("api.rest", mod.SessionModule)
			if err != nil {
				return err
			}

			mod.Debug("%+v", cfg)
			mod.Info("generating TLS key to %s", mod.keyFile)
			mod.Info("generating TLS certificate to %s", mod.certFile)
			if err := tls.Generate(cfg, mod.certFile, mod.keyFile); err != nil {
				return err
			}
		} else {
			mod.Info("loading TLS key from %s", mod.keyFile)
			mod.Info("loading TLS certificate from %s", mod.certFile)
		}
	}

	mod.server.Addr = fmt.Sprintf("%s:%d", ip, port)

	router := mux.NewRouter()

	router.Methods("OPTIONS").HandlerFunc(mod.corsRoute)

	router.HandleFunc("/api/events", mod.eventsRoute)
	router.HandleFunc("/api/session", mod.sessionRoute)
	router.HandleFunc("/api/session/ble", mod.sessionRoute)
	router.HandleFunc("/api/session/ble/{mac}", mod.sessionRoute)
	router.HandleFunc("/api/session/hid", mod.sessionRoute)
	router.HandleFunc("/api/session/hid/{mac}", mod.sessionRoute)
	router.HandleFunc("/api/session/env", mod.sessionRoute)
	router.HandleFunc("/api/session/gateway", mod.sessionRoute)
	router.HandleFunc("/api/session/interface", mod.sessionRoute)
	router.HandleFunc("/api/session/modules", mod.sessionRoute)
	router.HandleFunc("/api/session/lan", mod.sessionRoute)
	router.HandleFunc("/api/session/lan/{mac}", mod.sessionRoute)
	router.HandleFunc("/api/session/options", mod.sessionRoute)
	router.HandleFunc("/api/session/packets", mod.sessionRoute)
	router.HandleFunc("/api/session/started-at", mod.sessionRoute)
	router.HandleFunc("/api/session/wifi", mod.sessionRoute)
	router.HandleFunc("/api/session/wifi/{mac}", mod.sessionRoute)
	router.HandleFunc("/api/file", mod.fileRoute)

	mod.server.Handler = router

	if mod.username == "" || mod.password == "" {
		mod.Warning("api.rest.username and/or api.rest.password parameters are empty, authentication is disabled.")
	}

	return nil
}

func (mod *RestAPI) Start() error {
	if err := mod.Configure(); err != nil {
		return err
	}

	mod.SetRunning(true, func() {
		var err error

		if mod.isTLS() {
			mod.Info("api server starting on https://%s", mod.server.Addr)
			err = mod.server.ListenAndServeTLS(mod.certFile, mod.keyFile)
		} else {
			mod.Info("api server starting on http://%s", mod.server.Addr)
			err = mod.server.ListenAndServe()
		}

		if err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	})

	return nil
}

func (mod *RestAPI) Stop() error {
	return mod.SetRunning(false, func() {
		go func() {
			mod.quit <- true
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		mod.server.Shutdown(ctx)
	})
}
