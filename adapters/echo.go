package adapters

import (
	"context"
	"fmt"
	"github.com/fabriqs/go-micro/micro"
	"github.com/fabriqs/go-micro/schema"
	"github.com/fabriqs/go-micro/util/errors"
	"github.com/getsentry/sentry-go"
	sentryecho "github.com/getsentry/sentry-go/echo"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	echojwt "github.com/labstack/echo-jwt/v4"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	log "github.com/sirupsen/logrus"
	echoSwagger "github.com/swaggo/echo-swagger"
	"net/http"
	"reflect"
	"strings"
)

var validate *validator.Validate

//goland:noinspection GoTypeAssertionOnErrors
func Bind(c echo.Context, input interface{}) error {

	binder := &echo.DefaultBinder{}
	if err := binder.Bind(input, c); err != nil {
		return echo.NewHTTPError(
			http.StatusBadRequest,
			schema.ErrorResponse{
				Kind:    "input.bindind",
				Message: "invalid_request_payload",
				Errors:  err.Error(),
			})
	}
	if err := binder.BindHeaders(c, input); err != nil {
		return echo.NewHTTPError(
			http.StatusBadRequest,
			schema.ErrorResponse{
				Kind:    "input.bindind",
				Message: "invalid_request_payload",
				Errors:  err.Error(),
			})
	}

	if err := validate.Struct(input); err != nil {
		validationErrors := err.(validator.ValidationErrors)
		errs := map[string]string{}
		for _, e := range validationErrors {
			errs[e.Field()] = e.Tag()
		}
		return echo.NewHTTPError(
			http.StatusBadRequest,
			schema.ErrorResponse{
				Kind:    "validation",
				Message: "validation.failed",
				Errors:  errs,
			},
		)
	}

	return nil
}

// =================================================================================
// ECHO ROUTER ADAPTER
// =================================================================================

type echoRouterAdapter struct {
	micro.Router
	e *echo.Echo
}

func NewEchoAdapter(config micro.RouterConfig) micro.Router {
	e := echo.New()
	e.HideBanner = true

	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tenantId := c.Request().Header.Get("X-TenantId")
			if tenantId == "" {
				tenantId = micro.DefaultTenantId
			}
			auth := &micro.Authentication{
				Authenticated: false,
				TenantId:      tenantId,
			}
			c.Set(micro.AuthKey, auth)
			return next(c)
		}
	})
	e.Use(middleware.GzipWithConfig(middleware.GzipConfig{
		Level: 5,
	}))
	e.Use(middleware.RequestID())
	if config.Cors {
		e.Use(middleware.CORS())
	}
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Format: "method=${method}, uri=${uri}, status=${status}\n",
	}))
	e.Use(middleware.Recover())
	if config.SentryDsn != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn: config.SentryDsn,
			// Set TracesSampleRate to 1.0 to capture 100%
			// of transactions for performance monitoring.
			// We recommend adjusting this value in production,
			// TracesSampleRate: 1.0,
		}); err != nil {
			fmt.Printf("Sentry initialization failed: %v\n", err)
		}
		e.Use(sentryecho.New(sentryecho.Options{}))
	}
	if config.BodyLimit != "" {
		e.Use(middleware.BodyLimit(config.BodyLimit))
	}
	if config.Swagger {
		e.GET("/swagger/*", echoSwagger.WrapHandler)
	}
	/*if config.Prometheus != nil && config.Prometheus.Enabled {
		e.F(echoprometheus.NewMiddleware(config.Prometheus.Subsystem)) // adds middleware to gather metrics
		e.GET("/metrics", echoprometheus.NewHandler())                   // adds route to serve gathered metrics
	}*/
	if config.RemoveTrailSlash {
		e.Pre(middleware.RemoveTrailingSlash())
	}

	if config.TokenProvider != nil {
		e.Use(echojwt.WithConfig(echojwt.Config{
			SigningKey: []byte(config.TokenProvider.SigningKey()),
			ContextKey: micro.AuthKey,
			Skipper: func(c echo.Context) bool {
				// let the app decide which routes require authentication
				authz := c.Request().Header.Get("Authorization")
				if authz == "" || !strings.HasPrefix(authz, "Bearer ") {
					return true
				}
				return false
			},
			ErrorHandler: func(c echo.Context, err error) error {
				return c.JSON(http.StatusUnauthorized, err.Error())
			},
			SuccessHandler: func(c echo.Context) {
				user := c.Get(micro.AuthKey)
				if user == nil {
					return
				}
				token := user.(*jwt.Token)

				claims0 := token.Claims

				sub, _ := claims0.GetSubject()
				issuer, _ := claims0.GetIssuer()

				tenantId := micro.DefaultTenantId
				if config.MultiTenant && issuer != "" {
					tenantId = issuer
				}

				auth := &micro.Authentication{
					UserId:        sub,
					Authenticated: true,
					TenantId:      tenantId,
					Token: &micro.AuthToken{
						Issuer: issuer,
					},
				}

				//TODO: F depenedency injection (UserService)
				if claims0 != nil && reflect.TypeOf(claims0).String() == "jwt.MapClaims" {
					claims := claims0.(jwt.MapClaims)
					if value, ok := claims["roles"]; ok {
						auth.Roles = strings.Split(value.(string), ",")
					}
					if value, ok := claims["role"]; ok {
						auth.Roles = strings.Split(value.(string), ",")
					}
					if value, ok := claims["permissions"]; ok {
						auth.Permissions = strings.Split(value.(string), ",")
					}
					if value, ok := claims["name"]; ok {
						auth.Name = value.(string)
					}
					if value, ok := claims["email"]; ok {
						auth.Email = value.(string)
					}
					if value, ok := claims["phone"]; ok {
						auth.PhonerNumber = value.(string)
					} else if value, ok = claims["phone_number"]; ok {
						auth.PhonerNumber = value.(string)
					}
					auth.Claims = make(map[string]interface{})
					for key, value := range claims {
						auth.Claims[key] = value
					}
				}

				c.Set(micro.AuthKey, auth)
			},
		}))
	}

	e.GET("/health", func(c echo.Context) error {
		status := schema.NewHealthStatus()
		return c.JSON(http.StatusOK, status)
	})

	return &echoRouterAdapter{e: e}
}

func (r *echoRouterAdapter) Handler() http.Handler {
	return r.e
}

func (r *echoRouterAdapter) Start(addr string) error {
	return r.e.Start(addr)
}

func (r *echoRouterAdapter) Shutdown() error {
	return r.e.Shutdown(context.Background())
}

func (r *echoRouterAdapter) GET(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodGet, path, handler, filters)
}

func (r *echoRouterAdapter) POST(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodPost, path, handler, filters)
}

func (r *echoRouterAdapter) PUT(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodPut, path, handler, filters)
}

func (r *echoRouterAdapter) PATCH(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodPatch, path, handler, filters)
}

func (r *echoRouterAdapter) DELETE(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodDelete, path, handler, filters)
}

func (r *echoRouterAdapter) request(method string, path string, handler interface{}, filters []micro.MiddlewareFunc) {
	r.e.Match([]string{method}, path, func(c echo.Context) (err error) {
		defer func() {
			if err0 := recover(); err0 != nil {
				err = mapHttpResponse(err0.(error), c)
			}
		}()
		return handleRequest(c, handler)
	}, createMiddlewares(filters)...)
}

func (r *echoRouterAdapter) Group(path string, filters ...micro.MiddlewareFunc) micro.BaseRouter {
	return &echoGroupRoute{
		g: r.e.Group(path, createMiddlewares(filters)...),
	}
}

// =================================================================================
// ECHO GROUP ROUTE
// =================================================================================

type echoGroupRoute struct {
	micro.BaseRouter
	g   *echo.Group
	ctx micro.Ctx
}

func (r *echoGroupRoute) GET(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodGet, path, handler, filters)
}

func (r *echoGroupRoute) POST(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodPost, path, handler, filters)
}

func (r *echoGroupRoute) PUT(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodPut, path, handler, filters)
}

func (r *echoGroupRoute) PATCH(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodPatch, path, handler, filters)
}

func (r *echoGroupRoute) DELETE(path string, handler interface{}, filters ...micro.MiddlewareFunc) {
	r.request(http.MethodDelete, path, handler, filters)
}

func (r *echoGroupRoute) request(method string, path string, handler interface{}, filters []micro.MiddlewareFunc) {

	r.g.Match([]string{method}, path, func(c echo.Context) (err error) {
		defer func() {
			if err0 := recover(); err0 != nil {
				log.Error(err0)
				err = mapHttpResponse(err0.(error), c)
			}
		}()
		return handleRequest(c, handler)

	}, createMiddlewares(filters)...)
}

// =================================================================================
// GENERIC HANDLER
// =================================================================================

//goland:noinspection GoTypeAssertionOnErrors
func handleRequest(c echo.Context, handler interface{}) (err error) {
	var result interface{}

	handlerType := reflect.TypeOf(handler)

	if handlerType.Kind() != reflect.Func {
		return fmt.Errorf("controller method is not a function")
	}

	numIn := handlerType.NumIn()
	if numIn == 0 {
		return fmt.Errorf("controller method must have at least one argument (micro.Ctx)")
	}
	if numIn > 2 {
		return fmt.Errorf("controller method must have at most two arguments (micro.Ctx, input binding)")
	}

	firstArgType := handlerType.In(0)

	if firstArgType != reflect.TypeOf(micro.Ctx{}) {
		return fmt.Errorf("handler must be a function with the first argument of type micro.Ctx")
	}

	ctx := createRouteContext(c)
	tenantId := ctx.TenantId
	if tenantId == "" {
		tenantId = micro.DefaultTenantId
	} else {
		log.Debugf("current tenant_id is %s", tenantId)
	}

	return ctx.Tx(func(tx micro.Ctx) error {

		args := []reflect.Value{reflect.ValueOf(tx)}

		if numIn == 2 {
			inputType := handlerType.In(1)
			inputValue := reflect.New(inputType).Elem()
			modelInput := inputValue.Addr().Interface() //
			if err = Bind(c, modelInput); err != nil {
				log.Errorf("validation failed for %s\n%v", c.Request().RequestURI, err.Error())
				return err
			}
			args = append(args, inputValue)
		}

		handlerValue := reflect.ValueOf(handler)

		res := handlerValue.Call(args)

		if len(res) == 1 {
			result = res[0].Interface()
		} else if len(res) == 2 {
			if res[1].IsNil() {
				result = res[0].Interface()
			} else {
				return res[1].Interface().(error)
			}
		} else {
			return fmt.Errorf("invalid handler return type")
		}

		if err != nil {
			log.Errorf("Error while handling request %s -- %v", c.Request().RequestURI, err.Error())
			return mapHttpResponse(err, c)
		}
		return c.JSON(http.StatusOK, result)
	})
}

func mapHttpResponse(err error, c echo.Context) error {
	log.Errorf("error while handling request %s -- %v", c.Request().RequestURI, err.Error())
	if e, ok := err.(*errors.FunctionalError); ok {
		return c.JSON(http.StatusBadRequest, micro.ErrorResponse{
			Kind:    e.Kind,
			Error:   e.Message,
			Details: e.Details,
		})
	}
	if e, ok := err.(*errors.TechnicalError); ok {
		return c.JSON(http.StatusInternalServerError, micro.ErrorResponse{
			Kind:    e.Kind,
			Error:   e.Message,
			Details: e.Details,
		})
	}
	if e, ok := err.(*errors.ForbiddenError); ok {
		return c.JSON(http.StatusForbidden, micro.ErrorResponse{
			Kind:    e.Kind,
			Error:   e.Message,
			Details: e.Details,
		})
	}
	if e, ok := err.(*errors.UnauthorizedError); ok {
		return c.JSON(http.StatusUnauthorized, micro.ErrorResponse{
			Kind:    e.Kind,
			Error:   e.Message,
			Details: e.Details,
		})
	}
	if e, ok := err.(*errors.ResourceNotFoundError); ok {
		return c.JSON(http.StatusNotFound, micro.ErrorResponse{
			Kind:    e.Kind,
			Error:   e.Message,
			Details: e.Details,
		})
	}
	if e, ok := err.(*errors.ConflictError); ok {
		return c.JSON(http.StatusConflict, micro.ErrorResponse{
			Kind:    e.Kind,
			Error:   e.Message,
			Details: e.Details,
		})
	}
	if httpErr, ok := err.(*echo.HTTPError); ok {
		return c.JSON(httpErr.Code, httpErr.Message)
	}
	return err
}

// =================================================================================
// INIT
// =================================================================================

func createMiddlewares(filters []micro.MiddlewareFunc) []echo.MiddlewareFunc {
	middlewares := make([]echo.MiddlewareFunc, 0)
	for _, filter := range filters {
		middlewares = append(middlewares, func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := filter(createRouteContext(c)); err != nil {
					return mapHttpResponse(err, c)
				}
				return next(c)
			}
		})
	}
	return middlewares
}

func createRouteContext(c echo.Context) micro.Ctx {
	value := c.Get(micro.AuthKey)
	if value == nil {
		return micro.NewCtx(micro.DefaultTenantId)
	}
	auth := value.(*micro.Authentication)
	return micro.NewAuthCtx(auth)
}

func init() {
	validate = validator.New()
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
}
