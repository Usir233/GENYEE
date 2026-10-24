//go:build windows

package localsandbox

import (
	"github.com/Usir233/GENYEE/internal/localsandbox/core"
	"github.com/Usir233/GENYEE/internal/localsandbox/winhost"
)

func NewBackend() (core.Backend, error) { return winhost.New() }
