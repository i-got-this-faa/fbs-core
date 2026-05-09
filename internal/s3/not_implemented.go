package s3

import "net/http"

func (h *ObjectHandlers) NotImplemented(w http.ResponseWriter, r *http.Request) {
	WriteS3Error(w, r, http.StatusNotImplemented, codeNotImplemented, messageNotImplemented)
}
