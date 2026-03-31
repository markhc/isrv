package headers

import "net/http"

func AddCacheHeader(w http.ResponseWriter) {
	w.Header().Set("cdn-cache-control", "public, max-age=36000")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=36000")
	w.Header().Set("cache-control", "public, max-age=36000")
}

func SetContentDisposition(w http.ResponseWriter, fileName string, inlineContent bool) {
	dispositionType := "attachment"
	if inlineContent {
		dispositionType = "inline"
	}
	w.Header().Set("Content-Disposition", dispositionType+"; filename=\""+fileName+"\"")
}

func SetContentType(w http.ResponseWriter, contentType string) {
	w.Header().Set("Content-Type", contentType)
}

func SetHeaders(w http.ResponseWriter, fileName string, fileMetadata map[string]string, inlineContent bool, cachingEnabled bool) {
	if cachingEnabled {
		AddCacheHeader(w)
	}

	if contentType, ok := fileMetadata["Content-Type"]; ok {
		SetContentType(w, contentType)
	}

	SetContentDisposition(w, fileName, inlineContent)
}
