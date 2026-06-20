//! The NDJSON/stdio seam (GUI-RULES.md §2).
//!
//! One UTF-8 JSON object per line. stdout is ONLY protocol; all logs go to
//! stderr. The Go controller speaks this exact spec.
//!
//! in  (stdin) : {"type":"command"|"query","id":"<str>","name":"<verb>","args":{...}}
//! out (stdout): {"type":"response","id":"<same>","ok":true,"data":{...}}
//!               {"type":"response","id":"<same>","ok":false,"error":"..."}
//!               {"type":"event","name":"<verb>","data":{...}}

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// An inbound message from the engine. `type` is accepted but not load-bearing
/// for dispatch (the verb in `name` decides what runs); we keep it for parity
/// and future query/command divergence.
#[derive(Debug, Deserialize)]
pub struct Incoming {
    // The protocol `type` ("command"|"query"). Accepted for spec parity; dispatch
    // is by `name`. Kept on the struct as the documented wire contract.
    #[allow(dead_code)]
    #[serde(rename = "type")]
    pub kind: Option<String>,
    #[serde(default)]
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub args: Value,
}

/// A successful or failed response to a command/query.
#[derive(Debug, Serialize)]
pub struct Response {
    #[serde(rename = "type")]
    pub kind: &'static str, // always "response"
    pub id: String,
    pub ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

impl Response {
    pub fn ok(id: &str, data: Value) -> Self {
        Response {
            kind: "response",
            id: id.to_string(),
            ok: true,
            data: Some(data),
            error: None,
        }
    }

    pub fn err(id: &str, msg: impl Into<String>) -> Self {
        Response {
            kind: "response",
            id: id.to_string(),
            ok: false,
            data: None,
            error: Some(msg.into()),
        }
    }
}

/// An async push from sidecar to engine (startup `ready`, progress, etc.).
#[derive(Debug, Serialize)]
pub struct Event {
    #[serde(rename = "type")]
    pub kind: &'static str, // always "event"
    pub name: String,
    pub data: Value,
}

impl Event {
    pub fn new(name: &str, data: Value) -> Self {
        Event {
            kind: "event",
            name: name.to_string(),
            data,
        }
    }
}
