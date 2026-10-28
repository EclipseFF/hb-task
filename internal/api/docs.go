package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files/v2"
	"hb-task/docs"
)

func addDocs(router *gin.Engine) {
	router.GET("/openapi.yaml", func(c *gin.Context) { c.Data(200, "application/yaml", docs.OpenAPI) })
	assets := http.StripPrefix("/swagger/", http.FileServer(http.FS(swaggerFiles.FS)))
	router.GET("/swagger/*path", func(c *gin.Context) {
		if c.Param("path") == "/" {
			c.Data(200, "text/html; charset=utf-8", []byte(swaggerPage))
			return
		}
		assets.ServeHTTP(c.Writer, c.Request)
	})
}

const swaggerPage = `<!doctype html><html lang="ru"><head><meta charset="utf-8">
<title>Vacation API</title><link rel="stylesheet" href="/swagger/swagger-ui.css"></head>
<body><div id="swagger-ui"></div><script src="/swagger/swagger-ui-bundle.js"></script>
<script>SwaggerUIBundle({url:'/openapi.yaml',dom_id:'#swagger-ui'});</script></body></html>`
