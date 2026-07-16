import test from "node:test";
import assert from "node:assert/strict";
import {
  collectMCPProtocolOperations,
  collectToolSourceStats,
  filterTaskPlans,
  highestEventSeq,
  isTerminalTurnStatus,
  mcpDiagnosticBadges,
  mcpResultSummary,
  mergeEventResponses,
  taskPlanStatusCounts
} from "./utils.js";

test("isTerminalTurnStatus recognizes terminal turns", () => {
  for (const status of ["completed", "failed", "canceled", "terminated", "COMPLETED"]) {
    assert.equal(isTerminalTurnStatus(status), true);
  }
  for (const status of ["", "running", "waiting_approval", "pending"]) {
    assert.equal(isTerminalTurnStatus(status), false);
  }
});

test("mergeEventResponses appends, replaces, sorts, and preserves metadata", () => {
  const previous = {
    events: [{ seq: 1, type: "one" }, { seq: 3, type: "old-three" }],
    source: "initial",
    error: "old error"
  };
  const incoming = {
    events: [{ seq: 2, type: "two" }, { seq: 3, type: "three" }],
    source: "incremental"
  };

  const result = mergeEventResponses(previous, incoming);

  assert.deepEqual(result.events.map((event) => [event.seq, event.type]), [
    [1, "one"],
    [2, "two"],
    [3, "three"]
  ]);
  assert.equal(result.source, "incremental");
  assert.equal(Object.hasOwn(result, "error"), false);
  assert.equal(highestEventSeq(result.events), 3);
});

test("task Plan history counts known states and filters without reordering", () => {
  const plans = [
    { id: "plan-3", status: "active" },
    { id: "plan-2", status: "completed" },
    { id: "plan-1", status: "superseded" },
    { id: "plan-0", status: "future_status" }
  ];
  assert.deepEqual(taskPlanStatusCounts(plans), {
    total: 4,
    active: 1,
    completed: 1,
    canceled: 0,
    superseded: 1
  });
  assert.deepEqual(filterTaskPlans(plans, "completed").map((plan) => plan.id), ["plan-2"]);
  assert.deepEqual(filterTaskPlans(plans).map((plan) => plan.id), ["plan-3", "plan-2", "plan-1", "plan-0"]);
  assert.deepEqual(taskPlanStatusCounts(null), { total: 0, active: 0, completed: 0, canceled: 0, superseded: 0 });
});

test("mcpDiagnosticBadges summarizes non-sensitive MCP metadata", () => {
  assert.deepEqual(mcpDiagnosticBadges(null), []);
  assert.deepEqual(mcpDiagnosticBadges({
    tool_source: "mcp",
    mcp_transport: "streamable_http",
    mcp_protocol_version: "2025-06-18",
    mcp_capabilities: ["tools", "resources"],
    mcp_tool_count: 2,
    mcp_oauth: true,
    mcp_listen: true,
    mcp_expose_resources: true
  }), [
    "transport streamable_http",
    "protocol 2025-06-18",
    "2 tool(s)",
    "capabilities tools, resources",
    "OAuth",
    "SSE listener",
    "resources exposed"
  ]);
  assert.deepEqual(mcpDiagnosticBadges({ tool_source: "builtin", mcp_transport: "stdio" }), []);
});

test("mcpResultSummary summarizes MCP tool state without echoing result text", () => {
  assert.equal(mcpResultSummary(null), null);
  const summary = mcpResultSummary({
    tool_source: "mcp",
    state: {
      protocol_version: "tma.mcp_result.v1",
      tool_name: "filesystem.read_file",
      is_error: false,
      content: [{ type: "text", text: "secret body should stay out of summary" }, { type: "image", mime_type: "image/png", data_bytes: 12 }],
      structured_content: { private_value: "do not render" },
      meta: { request_id: "abc" }
    }
  });

  assert.equal(summary.title, "MCP tool result");
  assert.deepEqual(summary.facts, [
    "tool filesystem.read_file",
    "is_error false",
    "2 content item(s)",
    "content types text, image",
    "structured content present",
    "1 meta key(s)"
  ]);
  assert.equal(summary.facts.join(" ").includes("secret body"), false);
  assert.equal(summary.facts.join(" ").includes("private_value"), false);
});

test("mcpResultSummary summarizes MCP context bridge state", () => {
  assert.deepEqual(mcpResultSummary({
    tool_source: "mcp",
    state: {
      protocol_version: "tma.mcp_context_result.v1",
      tool_name: "__tma_mcp_read_resource",
      contents: [
        { uri: "fixture://guide", mimeType: "text/plain", text: "guide body" },
        { uri: "fixture://image", mime_type: "image/png", blob: "YmFzZTY0" }
      ]
    }
  }), {
    title: "MCP context result",
    facts: [
      "tool __tma_mcp_read_resource",
      "2 resource content item(s)",
      "mime text/plain, image/png",
      "1 text item(s)",
      "1 blob item(s)"
    ]
  });

  assert.deepEqual(mcpResultSummary({
    tool_source: "mcp",
    state: {
      protocol_version: "tma.mcp_context_result.v1",
      tool_name: "__tma_mcp_list_resource_templates",
      resource_templates: [
        { uriTemplate: "fixture://docs/{name}", name: "docs" },
        { uriTemplate: "fixture://images/{id}", name: "images" }
      ]
    }
  }), {
    title: "MCP context result",
    facts: ["tool __tma_mcp_list_resource_templates", "2 resource template(s)"]
  });

  assert.deepEqual(mcpResultSummary({
    tool_source: "mcp",
    state: {
      protocol_version: "tma.mcp_context_result.v1",
      tool_name: "__tma_mcp_get_prompt",
      prompt: { messages: [{ role: "user", content: { text: "prompt body" } }, { role: "assistant", content: { text: "reply" } }] }
    }
  }), {
    title: "MCP context result",
    facts: ["tool __tma_mcp_get_prompt", "2 prompt message(s)", "roles user, assistant"]
  });
});

test("collectToolSourceStats counts tool source metadata", () => {
  assert.deepEqual(collectToolSourceStats([
    { payload: { data: { tool_source: "mcp" } } },
    { payload: { data: { tool_source: "worker_plugin" } } },
    { payload: { data: { tool_source: "builtin" } } },
    { payload: { data: { tool_source: "custom" } } },
    { payload: { data: {} } }
  ]), { mcp: 1, worker_plugin: 1, builtin: 1, other: 1, total: 4 });
});

test("collectMCPProtocolOperations pairs calls and results into redacted protocol facts", () => {
  const operations = collectMCPProtocolOperations([
    {
      seq: 10,
      type: "runtime.tool_call",
      created_at: "2026-07-14T06:00:00Z",
      payload: { data: {
        id: "call_1",
        identifier: "filesystem",
        api_name: "read_file",
        tool_source: "mcp",
        mcp_transport: "streamable_http",
        mcp_protocol_version: "2025-06-18",
        mcp_capabilities: ["tools", "resources"],
        mcp_oauth: true,
        arguments: { token: "secret argument" },
        endpoint: "https://private.example/mcp",
        headers: { Authorization: "Bearer secret" }
      } }
    },
    {
      seq: 11,
      type: "runtime.tool_result",
      created_at: "2026-07-14T06:00:01Z",
      payload: { data: {
        id: "call_1",
        identifier: "filesystem",
        api_name: "read_file",
        tool_source: "mcp",
        duration_ms: 42,
        success: true,
        artifacts: [{ artifact_id: "art_1" }],
        state: {
          protocol_version: "tma.mcp_result.v1",
          tool_name: "readFile",
          is_error: false,
          content: [{ type: "text", text: "secret result body" }],
          structured_content: { private_value: "secret structured value" }
        }
      } }
    },
    {
      seq: 12,
      type: "runtime.tool_call",
      payload: { data: { id: "call_2", identifier: "docs", api_name: "mcp_read_resource", tool_source: "mcp" } }
    },
    {
      seq: 13,
      type: "runtime.tool_call",
      payload: { data: { id: "call_3", identifier: "docs", api_name: "mcp_list_resource_templates", tool_source: "mcp" } }
    }
  ]);

  assert.equal(operations.length, 3);
  assert.deepEqual(operations[0], {
    key: "call_1:10",
    call_id: "call_1",
    identifier: "filesystem",
    api_name: "read_file",
    method: "tools/call",
    status: "completed",
    request_seq: 10,
    response_seq: 11,
    request_time: "2026-07-14T06:00:00Z",
    response_time: "2026-07-14T06:00:01Z",
    duration_ms: 42,
    transport: "streamable_http",
    protocol_version: "2025-06-18",
    diagnostics: ["transport streamable_http", "protocol 2025-06-18", "capabilities tools, resources", "OAuth"],
    result_protocol: "tma.mcp_result.v1",
    result_summary: {
      title: "MCP tool result",
      facts: ["tool readFile", "is_error false", "1 content item(s)", "content types text", "structured content present"]
    },
    error_type: "",
    artifact_count: 1,
    preview_truncated: false
  });
  assert.equal(operations[1].method, "resources/read");
  assert.equal(operations[1].status, "pending");
  assert.equal(operations[2].method, "resources/templates/list");
  assert.equal(operations[2].status, "pending");

  const serialized = JSON.stringify(operations);
  for (const secret of ["secret argument", "private.example", "Bearer secret", "secret result body", "secret structured value", "private_value"]) {
    assert.equal(serialized.includes(secret), false);
  }
});

test("collectMCPProtocolOperations pairs repeated call ids in sequence and exposes only error type", () => {
  const operations = collectMCPProtocolOperations([
    { seq: 1, type: "runtime.tool_call", payload: { data: { id: "same", identifier: "one", api_name: "read", tool_source: "mcp" } } },
    { seq: 2, type: "runtime.tool_call", payload: { data: { id: "same", identifier: "two", api_name: "mcp_get_prompt", tool_source: "mcp" } } },
    { seq: 3, type: "runtime.tool_result", payload: { data: { id: "same", tool_source: "mcp", success: true, state: { protocol_version: "tma.mcp_result.v1", content: [] } } } },
    { seq: 4, type: "runtime.tool_result", payload: { data: { id: "same", tool_source: "mcp", error: { type: "permission_denied", message: "token leaked here" } } } }
  ]);

  assert.deepEqual(operations.map((item) => [item.identifier, item.method, item.response_seq, item.status, item.error_type]), [
    ["one", "tools/call", 3, "completed", ""],
    ["two", "prompts/get", 4, "failed", "permission_denied"]
  ]);
  assert.equal(JSON.stringify(operations).includes("token leaked here"), false);
});
