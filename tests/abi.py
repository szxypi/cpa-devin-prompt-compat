#!/usr/bin/env python3
"""Exercise the compiled plugin through CPA's public C ABI."""

import ctypes
import json


class Buffer(ctypes.Structure):
    _fields_ = [("ptr", ctypes.c_void_p), ("len", ctypes.c_size_t)]


PluginCall = ctypes.CFUNCTYPE(
    ctypes.c_int,
    ctypes.c_char_p,
    ctypes.POINTER(ctypes.c_uint8),
    ctypes.c_size_t,
    ctypes.POINTER(Buffer),
)
PluginFree = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_size_t)
PluginShutdown = ctypes.CFUNCTYPE(None)


class HostAPI(ctypes.Structure):
    _fields_ = [
        ("abi_version", ctypes.c_uint32),
        ("host_ctx", ctypes.c_void_p),
        ("call", ctypes.c_void_p),
        ("free_buffer", ctypes.c_void_p),
    ]


class PluginAPI(ctypes.Structure):
    _fields_ = [
        ("abi_version", ctypes.c_uint32),
        ("call", PluginCall),
        ("free_buffer", PluginFree),
        ("shutdown", PluginShutdown),
    ]


def invoke(plugin: PluginAPI, method: str, request: dict) -> dict:
    encoded = json.dumps(request, separators=(",", ":")).encode()
    request_buffer = (ctypes.c_uint8 * len(encoded)).from_buffer_copy(encoded)
    response = Buffer()
    rc = plugin.call(
        method.encode(), request_buffer, len(encoded), ctypes.byref(response)
    )
    try:
        raw = ctypes.string_at(response.ptr, response.len) if response.ptr else b""
    finally:
        if response.ptr:
            plugin.free_buffer(response.ptr, response.len)
    decoded = json.loads(raw)
    if rc != 0 or not decoded.get("ok"):
        raise RuntimeError(f"{method} failed: rc={rc}, response={decoded}")
    return decoded["result"]
