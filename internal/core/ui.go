package core

import (
	"embed"
	"fmt"
	"net/http"
)

//go:embed ui/index.html
var uiFiles embed.FS

func (a *APIServer) ui(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, err := uiFiles.ReadFile("ui/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = w.Write(data)
}
