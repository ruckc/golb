package golb

import (
	"bytes"
	"io/ioutil"
	"log"
	"net/http"
)

// responseCaptureWriter wraps http.ResponseWriter to capture response body
type responseCaptureWriter struct {
	http.ResponseWriter
	body *bytes.Buffer
}

func (w *responseCaptureWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// Lb is the main request handler, selecting a backend and proxying the request
func Lb(w http.ResponseWriter, r *http.Request, pool *ServerPool, accessLogEnabled bool, accessLogPayloads bool) {
	peer := pool.GetNextPeer(r.Context())
	if peer == nil {
		log.Printf("Service Unavailable: No healthy backends available for request %s %s", r.Method, r.URL.Path)
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	if accessLogEnabled {
		log.Printf("Forwarding %s %s to backend %s", r.Method, r.URL.Path, peer.URL)
		if accessLogPayloads {
			// Read and log request body
			var reqBodyBytes []byte
			if r.Body != nil {
				reqBodyBytes, _ = ioutil.ReadAll(r.Body)
				log.Printf("Request Body: %s", string(reqBodyBytes))
				// Restore the io.ReadCloser to its original state
				r.Body = ioutil.NopCloser(bytes.NewBuffer(reqBodyBytes))
			}
		}
	}

	// Wrap ResponseWriter to capture response body if payload logging enabled
	var rw http.ResponseWriter = w
	var respBody *bytes.Buffer
	if accessLogEnabled && accessLogPayloads {
		respBody = &bytes.Buffer{}
		rw = &responseCaptureWriter{ResponseWriter: w, body: respBody}
	}

	peer.ReverseProxy.ServeHTTP(rw, r)

	if accessLogEnabled && accessLogPayloads && respBody != nil {
		log.Printf("Response Body: %s", respBody.String())
	}
}
