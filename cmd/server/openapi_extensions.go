// Copyright © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT
//
// This file contains the user-editable OpenAPI extension hook.
//
// ✅ This file is safe to edit: it will NOT be overwritten by regeneration.
//
// Add any routes that are not Fabrica-generated (legacy APIs, custom endpoints,
// WireGuard, cloud-init, etc.) to registerCustomOpenAPIPaths so they appear in
// the served OpenAPI spec and Swagger UI at /openapi.json and /docs.
//
// Example:
//
//	func registerCustomOpenAPIPaths(spec *openapi3.T) {
//	    metaDataOp := openapi3.NewOperation()
//	    metaDataOp.OperationID = "getMetaData"
//	    metaDataOp.Summary = "Cloud-init meta-data endpoint"
//	    metaDataOp.Tags = []string{"cloud-init"}
//	    metaDataOp.Responses = openapi3.NewResponses()
//	    metaDataOp.Responses.Set("200", &openapi3.ResponseRef{
//	        Value: openapi3.NewResponse().WithDescription("YAML metadata for the requesting node"),
//	    })
//	    spec.Paths.Set("/meta-data", &openapi3.PathItem{Get: metaDataOp})
//	}
package main

import (
	"io"
	"net/http"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/user/firmware-updater/pkg/firmwareproxy"
)

// registerCustomOpenAPIPaths is called by GenerateOpenAPISpec after all
// Fabrica-generated resource paths have been registered.
// Add your custom / non-generated route definitions here.
func registerCustomOpenAPIPaths(spec *openapi3.T) {
	op := openapi3.NewOperation()
	op.OperationID = "getFirmwareProxyLayer"
	op.Summary = "Stream firmware payload layer by digest"
	op.Tags = []string{"Firmware Proxy"}
	op.Responses = openapi3.NewResponses()
	op.Responses.Set("200", &openapi3.ResponseRef{
		Value: openapi3.NewResponse().WithDescription("Firmware payload bytes"),
	})
	op.Responses.Set("400", &openapi3.ResponseRef{
		Value: openapi3.NewResponse().WithDescription("Invalid digest"),
	})
	op.Responses.Set("404", &openapi3.ResponseRef{
		Value: openapi3.NewResponse().WithDescription("Unknown digest"),
	})
	op.Responses.Set("503", &openapi3.ResponseRef{
		Value: openapi3.NewResponse().WithDescription("OCI backend unavailable"),
	})

	digestParam := openapi3.NewPathParameter("digest")
	digestParam.Description = "OCI layer digest (for example sha256:...)"
	digestParam.Required = true
	digestParam.Schema = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
	op.Parameters = append(op.Parameters, &openapi3.ParameterRef{Value: digestParam})

	spec.Paths.Set("/firmware-proxy/layer/{digest}", &openapi3.PathItem{Get: op})
}

func registerFirmwareProxyRoute(r chi.Router, resolver *firmwareproxy.Resolver) {
	if resolver == nil {
		resolver = firmwareproxy.NewResolver("", "", "")
	}

	handler := func(w http.ResponseWriter, req *http.Request) {
		digest := chi.URLParam(req, "digest")
		rc, size, err := resolver.StreamPayloadLayer(req.Context(), digest)
		if err != nil {
			if statusErr, ok := err.(*firmwareproxy.HTTPStatusError); ok {
				http.Error(w, statusErr.Error(), statusErr.StatusCode)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rc.Close()

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))

		if req.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
	}

	r.Get("/firmware-proxy/layer/{digest}", handler)
	r.Head("/firmware-proxy/layer/{digest}", handler)
}
