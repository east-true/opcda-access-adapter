package http

import (
	"context"
	stdhttp "net/http"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type browseHTTPRequest struct {
	Path   []exactJSONString `json:"path"`
	Filter string            `json:"filter"`
}

type browseHTTPEntry struct {
	Kind         string                `json:"kind"`
	Name         string                `json:"name"`
	ItemID       *string               `json:"itemId"`
	DataType     *opcda.DAVarTypeInfo  `json:"dataType,omitempty"`
	AccessRights *opcda.DAAccessRights `json:"accessRights,omitempty"`
}

func (s *Server) handleBrowse(ctx context.Context, w stdhttp.ResponseWriter, request *stdhttp.Request) {
	if !validateJSONRequest(w, request) {
		return
	}
	var decoded browseHTTPRequest
	if err := s.decodeRequestBody(w, request, &decoded); err != nil {
		writeDecodeError(w, err)
		return
	}
	if decoded.Filter == "" {
		decoded.Filter = string(opcda.BrowseFilterAll)
	}
	filter := opcda.BrowseFilter(decoded.Filter)
	if filter != opcda.BrowseFilterAll && filter != opcda.BrowseFilterBranch && filter != opcda.BrowseFilterItem {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "filter must be all, branch, or item")
		return
	}
	if len(decoded.Path) > s.config.MaxBrowseDepth {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeRequestLimitExceeded, "Browse path depth limit exceeded")
		return
	}
	path := make([]string, len(decoded.Path))
	for index, segment := range decoded.Path {
		if segment == "" {
			writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "browse path segments must not be empty")
			return
		}
		if len([]byte(segment)) > s.config.MaxItemIDBytes {
			writeError(w, stdhttp.StatusBadRequest, opcda.CodeItemIDTooLong, "browse path segment exceeds configured limit")
			return
		}
		for _, character := range segment {
			if character == 0 {
				writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "browse path must not contain NUL")
				return
			}
		}
		path[index] = string(segment)
	}

	result, err := s.runtime.Browse(ctx, opcda.BrowseRequest{Path: path, Filter: filter})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if len(result.Entries) > s.config.MaxBrowseEntries {
		writeLayerError(w, stdhttp.StatusUnprocessableEntity, "adapter", opcda.CodeBrowseResultLimitExceeded, "Browse result limit exceeded", nil)
		return
	}
	entries := make([]browseHTTPEntry, len(result.Entries))
	for index, entry := range result.Entries {
		entries[index] = browseHTTPEntry{Kind: string(entry.Kind), Name: entry.Name, AccessRights: entry.AccessRights}
		if entry.ItemID != nil {
			itemID := string(*entry.ItemID)
			entries[index].ItemID = &itemID
		}
		if entry.CanonicalType != nil {
			dataType := entry.CanonicalType.Information()
			entries[index].DataType = &dataType
		}
	}
	writeJSON(w, stdhttp.StatusOK, struct {
		Path    []string          `json:"path"`
		Entries []browseHTTPEntry `json:"entries"`
	}{Path: result.Path, Entries: entries})
}
