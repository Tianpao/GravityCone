//go:build !et_ffi

package cli

func (h *Handler) handleAddPeers(req Request) {
	rawPeers, ok := req.Params["peers"]
	if !ok {
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, "missing required parameter: peers"))
		return
	}
	peersArr, ok := rawPeers.([]any)
	if !ok {
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, "parameter peers must be an array of strings"))
		return
	}
	var addrs []string
	for _, v := range peersArr {
		s, ok := v.(string)
		if !ok {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, "parameter peers must be an array of strings"))
			return
		}
		if s != "" {
			addrs = append(addrs, s)
		}
	}
	if len(addrs) == 0 {
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, "peers array must not be empty"))
		return
	}
	h.scaffoldingSvc.AddPeers(addrs)
	h.paperConnectSvc.AddPeers(addrs)
	h.writer.WriteResponse(successResponse(req.ID, map[string]any{}))
}
