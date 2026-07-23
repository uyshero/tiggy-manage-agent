import Papa from "papaparse";

export const DATASET_MAX_ITEMS = 20;

const promptKeys = ["prompt", "提示词"];
const expectedOutputKeys = ["expected_output", "expectedOutput", "期望结果"];
const tagsKeys = ["tags", "标签"];

function field(record, keys) {
  for (const key of keys) {
    if (record?.[key] !== undefined && record[key] !== null) return record[key];
  }
  return "";
}

function baseName(filename) {
  return String(filename || "导入数据集")
    .replace(/^.*[\\/]/, "")
    .replace(/\.[^.]+$/, "")
    .trim() || "导入数据集";
}

function normalizedTags(value, rowNumber) {
  const source = Array.isArray(value) ? value : String(value || "").split(/[|,，;；]/);
  const tags = [];
  const seen = new Set();
  for (const raw of source) {
    const tag = String(raw || "").trim();
    if (!tag || seen.has(tag)) continue;
    if ([...tag].length > 80) throw new Error(`第 ${rowNumber} 行的标签不能超过 80 个字符。`);
    seen.add(tag);
    tags.push(tag);
  }
  if (tags.length > 10) throw new Error(`第 ${rowNumber} 行最多只能包含 10 个标签。`);
  return tags;
}

function normalizeItems(records) {
  if (!Array.isArray(records) || records.length === 0) throw new Error("导入文件中没有可用样本。");
  if (records.length > DATASET_MAX_ITEMS) throw new Error(`一个数据集最多包含 ${DATASET_MAX_ITEMS} 条样本，当前文件包含 ${records.length} 条。`);
  const seenPrompts = new Map();
  return records.map((record, index) => {
    const rowNumber = index + 1;
    const prompt = String(field(record, promptKeys) || "").trim();
    const expectedOutput = String(field(record, expectedOutputKeys) || "").trim();
    if (!prompt) throw new Error(`第 ${rowNumber} 行缺少提示词。`);
    if ([...prompt].length > 20000) throw new Error(`第 ${rowNumber} 行的提示词不能超过 20000 个字符。`);
    if ([...expectedOutput].length > 20000) throw new Error(`第 ${rowNumber} 行的期望结果不能超过 20000 个字符。`);
    const duplicateKey = prompt.toLocaleLowerCase();
    if (seenPrompts.has(duplicateKey)) throw new Error(`第 ${rowNumber} 行的提示词与第 ${seenPrompts.get(duplicateKey)} 行重复。`);
    seenPrompts.set(duplicateKey, rowNumber);
    return { prompt, expectedOutput, tags: normalizedTags(field(record, tagsKeys), rowNumber) };
  });
}

export function parseDatasetFile(text, filename) {
  const content = String(text || "").replace(/^\uFEFF/, "");
  const extension = String(filename || "").toLowerCase().split(".").pop();
  if (extension === "json") {
    let decoded;
    try {
      decoded = JSON.parse(content);
    } catch (error) {
      throw new Error(`JSON 解析失败：${error.message}`);
    }
    const envelope = Array.isArray(decoded) ? { items: decoded } : decoded;
    if (!envelope || typeof envelope !== "object" || !Array.isArray(envelope.items)) {
      throw new Error("JSON 必须是样本数组，或包含 items 数组的对象。");
    }
    return {
      filename,
      format: "json",
      name: String(envelope.name || "").trim() || baseName(filename),
      description: String(envelope.description || "").trim(),
      items: normalizeItems(envelope.items)
    };
  }
  if (extension !== "csv") throw new Error("仅支持 .csv 和 .json 文件。");
  const parsed = Papa.parse(content, {
    header: true,
    skipEmptyLines: "greedy",
    transformHeader: (header) => String(header || "").replace(/^\uFEFF/, "").trim()
  });
  if (parsed.errors.length) {
    const issue = parsed.errors[0];
    throw new Error(`CSV 解析失败${Number.isInteger(issue.row) ? `（第 ${issue.row + 2} 行）` : ""}：${issue.message}`);
  }
  const headers = parsed.meta.fields || [];
  if (!headers.some((header) => promptKeys.includes(header))) throw new Error("CSV 缺少 prompt 或“提示词”列。");
  return {
    filename,
    format: "csv",
    name: baseName(filename),
    description: "",
    items: normalizeItems(parsed.data)
  };
}

function datasetItems(dataset) {
  return (dataset?.items || []).map((item) => ({
    prompt: String(item.prompt || ""),
    expected_output: String(item.expected_output ?? item.expectedOutput ?? ""),
    tags: Array.isArray(item.tags) ? item.tags : normalizedTags(item.tags, Number(item.item_index || 0) + 1)
  }));
}

function safeFilename(value) {
  return String(value || "export").trim().replace(/[\\/:*?"<>|]+/g, "-").replace(/\s+/g, "-").slice(0, 100) || "export";
}

export function serializeDataset(dataset, format = "json") {
  const items = datasetItems(dataset);
  if (format === "csv") {
    return {
      content: Papa.unparse(items.map((item) => ({ ...item, tags: item.tags.join("|") })), { columns: ["prompt", "expected_output", "tags"] }),
      filename: `${safeFilename(dataset?.name || "dataset")}.csv`,
      mimeType: "text/csv;charset=utf-8"
    };
  }
  return {
    content: JSON.stringify({ name: dataset?.name || "", description: dataset?.description || "", items }, null, 2),
    filename: `${safeFilename(dataset?.name || "dataset")}.json`,
    mimeType: "application/json;charset=utf-8"
  };
}

export function serializeDatasetTemplate() {
  return {
    content: Papa.unparse([{ prompt: "说明需要评测的问题", expected_output: "可选的期望结果", tags: "回归|核心" }], { columns: ["prompt", "expected_output", "tags"] }),
    filename: "llm-space-dataset-template.csv",
    mimeType: "text/csv;charset=utf-8"
  };
}

export function serializeExperiment(experiment, format = "json") {
  const items = (experiment?.items || []).map((item) => ({
    item_index: Number(item.item_index || 0) + 1,
    prompt: item.prompt || "",
    expected_output: item.expected_output || "",
    tags: Array.isArray(item.tags) ? item.tags : [],
    status: item.status || "",
    left_average: Number(item.left_average || 0),
    right_average: Number(item.right_average || 0),
    conclusion: item.conclusion || "",
    error_message: item.error_message || "",
    left_session_id: item.left_session_id || "",
    left_turn_id: item.left_turn_id || "",
    right_session_id: item.right_session_id || "",
    right_turn_id: item.right_turn_id || "",
    comparison_url: item.left_session_id && item.right_session_id
      ? `/space#left=${encodeURIComponent(item.left_session_id)}&left_turn=${encodeURIComponent(item.left_turn_id || "")}&right=${encodeURIComponent(item.right_session_id)}&right_turn=${encodeURIComponent(item.right_turn_id || "")}`
      : ""
  }));
  if (format === "csv") {
    return {
      content: Papa.unparse(items.map((item) => ({ ...item, tags: item.tags.join("|") }))),
      filename: `${safeFilename(experiment?.name || "experiment")}-results.csv`,
      mimeType: "text/csv;charset=utf-8"
    };
  }
  return {
    content: JSON.stringify({
      id: experiment?.id || "",
      name: experiment?.name || "",
      status: experiment?.status || "",
      dataset_id: experiment?.dataset_id || "",
      rubric_id: experiment?.rubric_id || "",
      summary: experiment?.summary || {},
      items
    }, null, 2),
    filename: `${safeFilename(experiment?.name || "experiment")}-results.json`,
    mimeType: "application/json;charset=utf-8"
  };
}
