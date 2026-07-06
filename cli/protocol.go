package cli

// Request represents an incoming JSON request from stdin.
type Request struct {
	ID     int                    `json:"id"`
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// Response represents an outgoing JSON response to stdout.
type Response struct {
	ID     int        `json:"id"`
	Status string     `json:"status"` // "success", "error", "progress"
	Data   interface{} `json:"data,omitempty"`
	Error  *ErrorInfo  `json:"error,omitempty"`
}

// ErrorInfo contains error details for failed requests.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Event represents an asynchronous event pushed to stdout.
type Event struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// Error code constants matching the CLI specification.
const (
	ErrSTUNFailed       = "STUN_FAILED"
	ErrSTUNParseError   = "STUN_PARSE_ERROR"
	ErrEasytierNotFound = "EASYTIER_NOT_FOUND"
	ErrInvalidMethod    = "INVALID_METHOD"
	ErrInvalidParams    = "INVALID_PARAMS"
	ErrNotConnected     = "NOT_CONNECTED"
	ErrRoomNotFound     = "ROOM_NOT_FOUND"
	ErrRoomAlreadyRun   = "ROOM_ALREADY_RUNNING"
	ErrInternalError    = "INTERNAL_ERROR"
)

func successResponse(id int, data interface{}) Response {
	return Response{ID: id, Status: "success", Data: data}
}

func errorResponse(id int, code string, message string) Response {
	return Response{ID: id, Status: "error", Error: &ErrorInfo{Code: code, Message: message}}
}

func progressResponse(id int, data interface{}) Response {
	return Response{ID: id, Status: "progress", Data: data}
}
