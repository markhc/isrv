package headers

import "net/http"

// AddCacheHeader sets long-lived cache-control headers on the response.
func AddCacheHeader(w http.ResponseWriter) {
	w.Header().Set("cdn-cache-control", "public, max-age=36000")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=36000")
	w.Header().Set("cache-control", "public, max-age=36000")
}

// SetContentDisposition sets the Content-Disposition header, using "inline" when
// inlineContent is true and "attachment" otherwise.
func SetContentDisposition(w http.ResponseWriter, fileName string, inlineContent bool) {
	dispositionType := "attachment"
	if inlineContent {
		dispositionType = "inline"
	}
	w.Header().Set("Content-Disposition", dispositionType+"; filename=\""+fileName+"\"")
}

// SetContentType sets the Content-Type header on the response.
func SetContentType(w http.ResponseWriter, contentType string) {
	w.Header().Set("Content-Type", contentType)
}

// SetHeaders applies caching, content-type, and content-disposition headers
// based on the provided file metadata and options.
func SetHeaders(
	w http.ResponseWriter,
	fileName string,
	fileMetadata map[string]string,
	inlineContent bool,
	cachingEnabled bool,
) {
	if cachingEnabled {
		AddCacheHeader(w)
	}

	if contentType, ok := fileMetadata["Content-Type"]; ok {
		SetContentType(w, contentType)
	}

	SetContentDisposition(w, fileName, inlineContent)
}
