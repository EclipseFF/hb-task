package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"hb-task/internal/core"
	"hb-task/internal/integration/auth"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

type API struct {
	Service  core.Service
	Verifier auth.Verifier
	Ready    gin.HandlerFunc
}

func (a API) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	addDocs(router)

	router.Use(gin.Recovery(), cors)
	_ = router.SetTrustedProxies(nil)

	router.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	router.GET("/readyz", a.Ready)

	employee := router.Group("/api", auth.Require(a.Verifier, "employee"))
	employee.GET("/me", a.me)
	employee.POST("/vacation-requests", a.submit)
	employee.GET("/vacation-requests", func(c *gin.Context) { a.list(c, auth.FromContext(c.Request.Context()).Subject) })
	router.GET("/api/manager/vacation-requests", auth.Require(a.Verifier, "manager"), func(c *gin.Context) { a.list(c, "") })

	return router
}

type submitDTO struct {
	StartDate string `json:"start_date" binding:"required,datetime=2006-01-02"`
	EndDate   string `json:"end_date" binding:"required,datetime=2006-01-02"`
	Comment   string `json:"comment" binding:"max=1000"`
}

func (a API) submit(c *gin.Context) {
	if c.ContentType() != "application/json" {
		c.JSON(415, gin.H{"error": "Content-Type must be application/json"})
		return
	}

	d := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10))
	d.DisallowUnknownFields()
	var dto submitDTO
	if err := d.Decode(&dto); err != nil {
		c.JSON(400, gin.H{"error": "invalid JSON body or unknown fields"})
		return
	}

	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		c.JSON(400, gin.H{"error": "expected a single JSON object"})
		return
	}

	if err := binding.Validator.ValidateStruct(dto); err != nil {
		c.JSON(400, gin.H{"error": "dates must be YYYY-MM-DD; comment cannot exceed 1000 characters"})
		return
	}

	input := core.SubmitInput{StartDate: dto.StartDate, EndDate: dto.EndDate, Comment: dto.Comment}
	request, created, err := a.Service.Submit(c.Request.Context(), auth.FromContext(c.Request.Context()).Subject, input, c.GetHeader("Idempotency-Key"), time.Now())
	if err != nil {
		handleError(c, err)
		return
	}

	code := http.StatusAccepted
	if !created {
		code = http.StatusOK
	}
	c.JSON(code, request)
}

func (a API) me(c *gin.Context) {
	employee, err := a.Service.Repo.Employee(c.Request.Context(), auth.FromContext(c.Request.Context()).Subject)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(200, employee)
}

func (a API) list(c *gin.Context, subject string) {
	var query struct {
		Limit  int `form:"limit,default=50" binding:"min=1,max=100"`
		Offset int `form:"offset,default=0" binding:"min=0"`
	}
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(400, gin.H{"error": "limit must be 1..100; offset must be >= 0"})
		return
	}
	requests, err := a.Service.Repo.List(c.Request.Context(), subject, query.Limit, query.Offset)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(200, requests)
}

func handleError(c *gin.Context, err error) {
	if validation, ok := errors.AsType[*core.ValidationError](err); ok {
		c.JSON(400, gin.H{"error": validation.Message})
		return
	}
	switch {
	case errors.Is(err, core.ErrNotFound):
		c.JSON(404, gin.H{"error": "employee or request not found"})
	case errors.Is(err, core.ErrConflict):
		c.JSON(409, gin.H{"error": err.Error()})
	default:
		slog.Error("API request failed", "error", err)
		c.JSON(500, gin.H{"error": "internal server error"})
	}
}

func cors(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-store")
	if c.GetHeader("Origin") == "http://localhost:3000" {
		c.Header("Access-Control-Allow-Origin", "http://localhost:3000")
		c.Header("Vary", "Origin")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
	}
	c.Next()
}
