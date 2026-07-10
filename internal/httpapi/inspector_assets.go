package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed inspector/*
var inspectorAssets embed.FS

func inspectorAssetHandler() http.Handler {
	subtree, err := fs.Sub(inspectorAssets, "inspector")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(subtree))
	return http.StripPrefix("/inspector/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}))
}
