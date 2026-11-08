package http

import (
	"embed"
	"github.com/gin-gonic/gin"
	"net/http"
)

//go:embed workflow-editor/index.html workflow-editor/app.js
var workflowEditor embed.FS

func registerWorkflowEditor(r *gin.Engine) {
	r.GET("/workflow-editor", func(c *gin.Context) {
		b, err := workflowEditor.ReadFile("workflow-editor/index.html")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", b)
	})
	r.GET("/workflow-editor/app.js", func(c *gin.Context) {
		b, err := workflowEditor.ReadFile("workflow-editor/app.js")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", b)
	})
}
