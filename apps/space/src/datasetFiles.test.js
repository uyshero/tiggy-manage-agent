import assert from "node:assert/strict";
import test from "node:test";
import Papa from "papaparse";
import { parseDatasetFile, serializeDataset, serializeDatasetTemplate, serializeExperiment } from "./datasetFiles.js";

test("CSV datasets support quoted multiline fields and normalized tags", () => {
  const parsed = parseDatasetFile('prompt,expected_output,tags\n"First, prompt","Line one\nLine two","core| regression |core"', "quality.csv");
  assert.equal(parsed.name, "quality");
  assert.deepEqual(parsed.items, [{ prompt: "First, prompt", expectedOutput: "Line one\nLine two", tags: ["core", "regression"] }]);
});

test("JSON datasets support envelopes and reject duplicate prompts", () => {
  const parsed = parseDatasetFile(JSON.stringify({ name: "中文数据集", items: [{ prompt: "问题", expected_output: "答案", tags: ["核心"] }] }), "dataset.json");
  assert.equal(parsed.name, "中文数据集");
  assert.equal(parsed.items[0].expectedOutput, "答案");
  assert.throws(
    () => parseDatasetFile(JSON.stringify([{ prompt: "Same" }, { prompt: " same " }]), "duplicates.json"),
    /与第 1 行重复/
  );
});

test("dataset and experiment exports preserve evidence fields", () => {
  const datasetCSV = serializeDataset({ name: "Regression", items: [{ prompt: "Question", expected_output: "Answer", tags: ["core", "db"] }] }, "csv");
  assert.deepEqual(Papa.parse(datasetCSV.content, { header: true }).data[0], { prompt: "Question", expected_output: "Answer", tags: "core|db" });
  assert.match(serializeDatasetTemplate().content, /expected_output/);

  const experimentJSON = JSON.parse(serializeExperiment({
    id: "experiment/1",
    name: "Nightly",
    items: [{ item_index: 0, prompt: "Question", status: "completed", left_session_id: "left/1", left_turn_id: "turn/1", right_session_id: "right/1", right_turn_id: "turn/2" }]
  }).content);
  assert.equal(experimentJSON.items[0].comparison_url, "/space#left=left%2F1&left_turn=turn%2F1&right=right%2F1&right_turn=turn%2F2");
});
