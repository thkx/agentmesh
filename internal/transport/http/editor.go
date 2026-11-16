package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	webassets "github.com/thkx/agentmesh/web"
)

// registerWorkflowEditor serves the editor's source assets from the shared web
// embed filesystem. Browser-facing files live under the repository-root web/ directory.
func registerWorkflowEditor(r *gin.Engine) {
	r.GET("/workflow-editor", func(c *gin.Context) {
		b, err := webassets.Assets.ReadFile("workflow-editor/index.html")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", b)
	})
	r.GET("/workflow-editor/app.js", func(c *gin.Context) {
		b, err := webassets.Assets.ReadFile("workflow-editor/app.js")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", b)
	})
}
