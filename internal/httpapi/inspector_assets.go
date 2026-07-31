package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed inspector/* app/* space/* knowledge/* console/*
var inspectorAssets embed.FS

func inspectorAssetHandler() http.Handler {
	return assetHandler("inspector", "/inspector/assets/")
}

func consoleAssetHandler() http.Handler {
	return assetHandler("console", "/console/assets/")
}

func appAssetHandler() http.Handler {
	return assetHandler("app", "/app/assets/")
}

func spaceAssetHandler() http.Handler {
	return assetHandler("space", "/space/assets/")
}

func knowledgeAssetHandler() http.Handler {
	return assetHandler("knowledge", "/knowledge/assets/")
}

func assetHandler(assetRoot string, routePrefix string) http.Handler {
	subtree, err := fs.Sub(inspectorAssets, assetRoot)
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(subtree))
	return http.StripPrefix(routePrefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}))
}
