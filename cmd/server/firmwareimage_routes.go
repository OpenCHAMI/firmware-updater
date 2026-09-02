package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/user/firmware-updater/pkg/firmwareimages"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

type firmwareImageListResponse struct {
	Items []firmwareimages.FirmwareImage `json:"items"`
	Count int                            `json:"count"`
}

// RegisterFirmwareImageRoutes registers OCI-backed firmware image endpoints.
func RegisterFirmwareImageRoutes(r chi.Router, registryHost string) {
	r.Route("/firmwareimages", func(r chi.Router) {
		r.Get("/", listFirmwareImagesHandler(registryHost))
		r.Get("/search", searchFirmwareImagesHandler(registryHost))
		r.Get("/image", getFirmwareImageHandler)
		r.Post("/", pushFirmwareImageHandler)
		r.Delete("/", deleteFirmwareImageHandler)
	})
}

func listFirmwareImagesHandler(registryHost string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		images, err := firmwareimages.ListFirmwareImages(r.Context(), registryHost, r.URL.Query().Get("repository"))
		if err != nil {
			writeFirmwareImageError(w, err)
			return
		}
		resources := make([]firmwareimages.FirmwareImage, 0, len(images))
		for _, image := range images {
			resources = append(resources, firmwareimages.FabricaResource(image))
		}
		writeJSON(w, http.StatusOK, resources)
	}
}

func searchFirmwareImagesHandler(registryHost string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		latest, err := firmwareimages.ParseLatest(query.Get("latest"))
		if err != nil {
			writeFirmwareImageError(w, err)
			return
		}
		filters := make(map[string]string)
		for _, key := range []string{"manufacturer", "deviceType", "model", "target", "softwareId", "version", "versionString", "tag", "filename"} {
			if value := strings.TrimSpace(query.Get(key)); value != "" {
				filters[key] = value
			}
		}
		images, err := firmwareimages.SearchFirmwareImages(r.Context(), registryHost, query.Get("repository"), filters, latest)
		if err != nil {
			writeFirmwareImageError(w, err)
			return
		}
		resources := make([]firmwareimages.FirmwareImage, 0, len(images))
		for _, image := range images {
			resources = append(resources, firmwareimages.FabricaResource(image))
		}
		writeJSON(w, http.StatusOK, resources)
	}
}

func getFirmwareImageHandler(w http.ResponseWriter, r *http.Request) {
	image, err := firmwareimages.GetFirmwareImage(r.Context(), r.URL.Query().Get("repository"), r.URL.Query().Get("tag"))
	if err != nil {
		writeFirmwareImageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, firmwareimages.FabricaResource(image))
}

func pushFirmwareImageHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart request: "+err.Error())
		return
	}

	var request firmwareimages.PushRequest
	if err := json.Unmarshal([]byte(r.FormValue("metadata")), &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata JSON: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required: "+err.Error())
		return
	}
	defer file.Close()

	image, err := firmwareimages.PushFirmwareImage(r.Context(), request, file, filepath.Base(header.Filename))
	if err != nil {
		writeFirmwareImageError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, firmwareimages.FabricaResource(image))
}

func deleteFirmwareImageHandler(w http.ResponseWriter, r *http.Request) {
	force, err := firmwareimages.ParseForce(r.URL.Query().Get("force"))
	if err != nil {
		writeFirmwareImageError(w, err)
		return
	}
	if err := firmwareimages.DeleteFirmwareImage(r.Context(), r.URL.Query().Get("repository"), r.URL.Query().Get("tag"), force); err != nil {
		writeFirmwareImageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeFirmwareImageError(w http.ResponseWriter, err error) {
	var statusError *firmwareproxy.HTTPStatusError
	if errors.As(err, &statusError) {
		writeError(w, statusError.StatusCode, statusError.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "firmware image operation failed")
}
