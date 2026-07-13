// Package main implements freebuff as a CLIProxyAPI (CPA) dynamic plugin.
//
// It ports freebuff2api's Freebuff/Codebuff adapter into the cliproxy C ABI:
// model provider + token auth + chat-completions executor (session / ads /
// agent-run chain / upstream SSE).
//
// Build (architecture must match the CPA host):
//
//	CGO_ENABLED=1 go build -buildmode=c-shared -o freebuff.so .
//	# Windows: freebuff.dll   macOS: freebuff.dylib
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

static int fb_call_host(cliproxy_host_api* api, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	return api->call(api->host_ctx, method, request, request_len, response);
}
static void fb_free_host_buffer(cliproxy_host_api* api, void* ptr, size_t len) {
	api->free_buffer(ptr, len);
}

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"sync"
	"unsafe"

	"github.com/WslzGmzs/freebuff2api/plugin"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

var (
	hostAPI    *C.cliproxy_host_api
	dispatcher *plugin.Dispatcher
	initOnce   sync.Once
)

func main() {}

func ensureDispatcher() *plugin.Dispatcher {
	initOnce.Do(func() {
		dispatcher = plugin.NewDispatcher(hostBridge{})
	})
	return dispatcher
}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, pluginAPI *C.cliproxy_plugin_api) C.int {
	if pluginAPI == nil {
		return 1
	}
	hostAPI = host
	pluginAPI.abi_version = C.uint32_t(pluginabi.ABIVersion)
	pluginAPI.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	pluginAPI.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	pluginAPI.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	ensureDispatcher()
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	methodName := C.GoString(method)
	var req []byte
	if request != nil && requestLen > 0 {
		req = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	out, err := ensureDispatcher().Handle(methodName, req)
	if err != nil {
		out = plugin.ErrorEnvelope("plugin_error", err.Error())
	}
	writeResponse(response, out)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

// hostBridge implements plugin.Host via CPA host callbacks.
type hostBridge struct{}

func (hostBridge) StreamEmit(streamID string, payload []byte) error {
	body, _ := json.Marshal(map[string]any{
		"stream_id": streamID,
		"payload":   payload,
	})
	_, err := hostCall(pluginabi.MethodHostStreamEmit, body)
	return err
}

func (hostBridge) StreamEmitError(streamID, message string) {
	body, _ := json.Marshal(map[string]any{
		"stream_id": streamID,
		"error":     message,
	})
	_, _ = hostCall(pluginabi.MethodHostStreamEmit, body)
}

func (hostBridge) StreamClose(streamID string) {
	body, _ := json.Marshal(map[string]any{"stream_id": streamID})
	_, _ = hostCall(pluginabi.MethodHostStreamClose, body)
}

func (hostBridge) Log(level, message string) {
	body, _ := json.Marshal(map[string]any{"level": level, "message": message})
	_, _ = hostCall(pluginabi.MethodHostLog, body)
}

func hostCall(method string, request []byte) ([]byte, error) {
	if hostAPI == nil || hostAPI.call == nil {
		return nil, fmt.Errorf("host api unavailable")
	}
	var resp C.cliproxy_buffer
	var reqPtr *C.uint8_t
	if len(request) > 0 {
		reqPtr = (*C.uint8_t)(unsafe.Pointer(&request[0]))
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	rc := C.fb_call_host(hostAPI, cMethod, reqPtr, C.size_t(len(request)), &resp)
	if rc != 0 {
		return nil, fmt.Errorf("host call %s failed rc=%d", method, int(rc))
	}
	if resp.ptr == nil || resp.len == 0 {
		return nil, nil
	}
	out := C.GoBytes(resp.ptr, C.int(resp.len))
	C.fb_free_host_buffer(hostAPI, resp.ptr, resp.len)
	return out, nil
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	buf := C.CBytes(raw)
	response.ptr = buf
	response.len = C.size_t(len(raw))
}
