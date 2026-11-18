package handler

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// hostProjectPicker opens the native folder dialog on Lite desktop.
// Nil on every other deployment: the route then 404s.
var (
	hostProjectPickerMu sync.RWMutex
	hostProjectPicker   func() (string, error)
)

// SetHostProjectPicker is called by cmd/desktop. Standard servers leave it nil.
func SetHostProjectPicker(fn func() (string, error)) {
	hostProjectPickerMu.Lock()
	hostProjectPicker = fn
	hostProjectPickerMu.Unlock()
}

func currentHostProjectPicker() func() (string, error) {
	hostProjectPickerMu.RLock()
	defer hostProjectPickerMu.RUnlock()
	return hostProjectPicker
}

// PickHostProjectDir opens the system folder picker. The SPA talks HTTP to
// this process; Wails JS bindings are not injected through the
// reverse-proxied frontend, so this is the working authorization path.
// PickHostProjectDir godoc
// @Summary      选择宿主机项目目录（Desktop）
// @Tags         系统
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/host-project-dir [post]
func (*SystemHandler) PickHostProjectDir(c *gin.Context) {
	picker := currentHostProjectPicker()
	if picker == nil {
		writeStatusError(c, http.StatusNotFound, "host project picker is not available")
		return
	}
	dir, err := picker()
	if err != nil {
		writeStatusError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{"dir": dir},
	})
}
