import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  Bot,
  Box,
  Check,
  ChevronDown,
  CircleAlert,
  CircleDot,
  Cloud,
  Code2,
  File,
  FileCode2,
  FileText,
  Folder,
  GitBranch,
  GitCommitHorizontal,
  LoaderCircle,
  MessageSquare,
  PanelLeft,
  Play,
  Plus,
  RefreshCw,
  Send,
  Server,
  Settings2,
  Sheet,
  Sparkles,
  Square,
  TerminalSquare
} from "lucide-react";

import { createAnalysisWorkspaceClient, createAnalysisWorkspaceRepository, DEFAULT_NOTEBOOK_CODE } from "./repository.js";
import {
  R_SURVIVAL_DATA_CLEANING_SKILL_CONTENT,
  R_SURVIVAL_DATA_CLEANING_SKILL_PATH
} from "./survivalSkill.js";
import "./styles.css";

const repositories = new Map();
const EXCEL_FILE_PATTERN = /\.(xlsx|xls)$/i;
let xlsxModulePromise;

const DEFAULT_CLEANING_FIELD_MAPPING = Object.freeze([
  Object.freeze({ source: "患者编号", target: "patient_id", type: "字符", semanticRole: "患者唯一标识", hint: "确认是否一人一行；重复编号需要说明合并/保留策略" }),
  Object.freeze({ source: "治疗组", target: "treatment", type: "分类", semanticRole: "分组变量", hint: "根据样本值和研究设计推断分组，不确定时生成待确认项" }),
  Object.freeze({ source: "随访月数", target: "followup_month", type: "数值", semanticRole: "生存时间", hint: "确认时间单位，必要时从天/年转换为月，并保留转换说明" }),
  Object.freeze({ source: "结局", target: "event", type: "0/1", semanticRole: "事件/删失", hint: "事件和删失不能硬猜；死亡、复发、进展、失访要按研究终点确认" }),
  Object.freeze({ source: "年龄", target: "age", type: "数值", semanticRole: "协变量", hint: "是否进入 Cox 模型由研究目标决定，异常年龄需列出" }),
  Object.freeze({ source: "分期", target: "stage", type: "有序分类", semanticRole: "协变量/分层变量", hint: "统一中文、罗马数字和数字分期；无法识别的值进入待确认项" }),
  Object.freeze({ source: "性别", target: "sex", type: "分类", semanticRole: "协变量", hint: "按样本值生成映射，非男/女取值不要直接丢弃" })
]);

const DEFAULT_RAW_FOLLOWUP_CSV = `患者编号,治疗组,随访月数,结局,年龄,分期,性别
P001,新治疗,18,死亡,62,III期,男
P002,标准治疗,24,存活,55,II期,女
P003,试验组,11,进展,48,四期,女性
P004,对照组,30,无事件,71,I期,男性
P005,新治疗,7,复发,66,III期,女
P006,标准治疗,20,失访,59,II期,男
`;

function isExcelFile(file) {
  return EXCEL_FILE_PATTERN.test(file?.name || "");
}

function loadXLSXModule() {
  if (!xlsxModulePromise) {
    xlsxModulePromise = import("xlsx");
  }
  return xlsxModulePromise;
}

function getExcelSheetPreview(xlsx, workbook, sheetName) {
  const worksheet = workbook.Sheets[sheetName];
  if (!worksheet) return [];
  return xlsx.utils
    .sheet_to_json(worksheet, { header: 1, blankrows: false, raw: false, defval: "" })
    .slice(0, 10)
    .map((row) => row.map((cell) => String(cell ?? "")));
}

async function readExcelImportDraft(file) {
  const buffer = await file.arrayBuffer();
  const xlsx = await loadXLSXModule();
  const workbook = xlsx.read(buffer, { type: "array", cellDates: true });
  const selectedSheet = workbook.SheetNames[0];
  if (!selectedSheet) throw new Error("Excel 文件没有可读取的工作表");
  return {
    fileName: file.name,
    workbook,
    selectedSheet,
    sheetNames: workbook.SheetNames,
    previewRows: getExcelSheetPreview(xlsx, workbook, selectedSheet),
    xlsx
  };
}

function buildExcelSheetImport(draft) {
  if (!draft?.selectedSheet) throw new Error("请选择要导入的 Excel 工作表");
  const worksheet = draft.workbook.Sheets[draft.selectedSheet];
  if (!worksheet) throw new Error(`找不到工作表：${draft.selectedSheet}`);
  return {
    content: draft.xlsx.utils.sheet_to_csv(worksheet, { blankrows: false }),
    sourceName: `${draft.fileName} · ${draft.selectedSheet}`
  };
}

async function readRawDataFile(file, encoding) {
  if (isExcelFile(file)) {
    throw new Error("Excel 文件需要先选择工作表并预览后导入");
  }
  const buffer = await file.arrayBuffer();
  const labels = encoding === "GBK" ? ["gb18030", "gbk"] : ["utf-8"];
  for (const label of labels) {
    try {
      return { content: new TextDecoder(label).decode(buffer).replace(/^\uFEFF/, ""), sourceName: file.name };
    } catch {
      // Try the next decoder label.
    }
  }
  return { content: await file.text(), sourceName: file.name };
}

function normalizeColumnName(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[\s_\-()（）[\]【】/\\.:：]/g, "");
}

function normalizeSampleValue(value) {
  return String(value || "").trim().toLowerCase();
}

function parseCSVRows(content, maxRows = 12) {
  const text = String(content || "");
  const rows = [];
  let row = [];
  let cell = "";
  let inQuotes = false;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (char === "\"") {
      if (inQuotes && text[index + 1] === "\"") {
        cell += "\"";
        index += 1;
      } else {
        inQuotes = !inQuotes;
      }
      continue;
    }
    if (!inQuotes && char === ",") {
      row.push(cell);
      cell = "";
      continue;
    }
    if (!inQuotes && (char === "\n" || char === "\r")) {
      if (char === "\r" && text[index + 1] === "\n") index += 1;
      row.push(cell);
      if (row.some((item) => String(item || "").trim())) rows.push(row);
      row = [];
      cell = "";
      if (rows.length >= maxRows) break;
      continue;
    }
    cell += char;
  }
  if (rows.length < maxRows && (cell.length || row.length)) {
    row.push(cell);
    if (row.some((item) => String(item || "").trim())) rows.push(row);
  }
  return rows;
}

function confidenceLabel(score) {
  if (score >= 10) return "高";
  if (score >= 6) return "中";
  if (score >= 4) return "低";
  return "";
}

function scoreColumnForTarget(column, target) {
  const header = normalizeColumnName(column.name);
  const samples = column.samples.map(normalizeSampleValue).filter(Boolean);
  const distinctSamples = new Set(samples);
  const numericValues = samples
    .map((value) => Number.parseFloat(value))
    .filter((value) => Number.isFinite(value));
  const numericRatio = samples.length ? numericValues.length / samples.length : 0;
  const reasons = [];
  let score = 0;

  const addHeaderScore = (patterns, points, reason) => {
    if (patterns.some((pattern) => pattern.test(header))) {
      score += points;
      reasons.push(reason);
      return true;
    }
    return false;
  };
  const addSampleScore = (condition, points, reason) => {
    if (condition) {
      score += points;
      reasons.push(reason);
    }
  };

  if (target === "patient_id") {
    addHeaderScore([/患者编号/, /病历号/, /病例号/, /受试者编号/, /patientid/, /^id$/, /subjectid/], 8, "表头像患者编号");
    addSampleScore(samples.length >= 2 && distinctSamples.size === samples.length, 2, "样本值基本唯一");
    addSampleScore(samples.some((value) => /^[a-z]+\d+$/i.test(value)), 2, "样本值像病例编号");
  }
  if (target === "treatment") {
    addHeaderScore([/治疗组/, /分组/, /组别/, /治疗方案/, /treatment/, /arm/, /group/], 8, "表头像分组列");
    addSampleScore(samples.some((value) => /新治疗|标准治疗|对照|试验|treat|control|arm/i.test(value)), 3, "样本值像治疗分组");
  }
  if (target === "followup_month") {
    addHeaderScore([/随访月数/, /随访时间/, /生存时间/, /观察时间/, /followup/, /month/, /months/, /time/, /^os$/, /^pfs$/, /^dfs$/], 8, "表头像随访时间");
    addSampleScore(numericRatio >= 0.8, 3, "样本值大多是数字");
    addSampleScore(numericValues.some((value) => value >= 1 && value <= 240), 2, "取值范围像月数");
  }
  if (target === "event") {
    addHeaderScore([/结局/, /事件/, /状态/, /终点/, /event/, /status/, /outcome/], 8, "表头像结局列");
    addSampleScore(samples.some((value) => /死亡|存活|无事件|进展|复发|失访|删失|event|alive|dead/.test(value)), 4, "样本值像事件/删失");
    addSampleScore(samples.length > 0 && samples.every((value) => ["0", "1"].includes(value)), 3, "样本值已经是 0/1");
  }
  if (target === "age") {
    addHeaderScore([/年龄/, /^age$/], 8, "表头像年龄列");
    addSampleScore(numericRatio >= 0.8, 2, "样本值大多是数字");
    addSampleScore(numericValues.some((value) => value >= 18 && value <= 100), 2, "取值范围像年龄");
  }
  if (target === "stage") {
    addHeaderScore([/分期/, /^stage$/, /tnm/], 8, "表头像分期列");
    addSampleScore(samples.some((value) => /i期|ii期|iii期|iv期|一期|二期|三期|四期|ⅰ|ⅱ|ⅲ|ⅳ|stage/i.test(value)), 4, "样本值像肿瘤分期");
  }
  if (target === "sex") {
    addHeaderScore([/性别/, /^sex$/, /^gender$/], 8, "表头像性别列");
    addSampleScore(samples.some((value) => /男|女|male|female/.test(value)), 4, "样本值像性别");
  }

  return { score, reasons };
}

function detectFieldSuggestions(content) {
  const rows = parseCSVRows(content, 10);
  if (!rows.length) return { columns: [], suggestions: [], previewRows: [] };
  const headers = rows[0].map((value, index) => String(value || "").trim() || `未命名列${index + 1}`);
  const dataRows = rows.slice(1);
  const columns = headers.map((name, index) => ({
    name,
    index,
    samples: dataRows.map((row) => String(row[index] || "").trim()).filter(Boolean).slice(0, 4)
  }));
  const targets = DEFAULT_CLEANING_FIELD_MAPPING.map((field) => field.target);
  const candidates = [];
  for (const column of columns) {
    for (const target of targets) {
      const result = scoreColumnForTarget(column, target);
      if (result.score >= 4) candidates.push({ ...result, target, source: column.name });
    }
  }
  candidates.sort((left, right) => right.score - left.score || left.source.localeCompare(right.source));
  const usedTargets = new Set();
  const usedSources = new Set();
  const suggestions = [];
  for (const candidate of candidates) {
    if (usedTargets.has(candidate.target) || usedSources.has(candidate.source)) continue;
    usedTargets.add(candidate.target);
    usedSources.add(candidate.source);
    suggestions.push({
      target: candidate.target,
      source: candidate.source,
      confidence: confidenceLabel(candidate.score),
      score: candidate.score,
      reasons: candidate.reasons
    });
  }
  return {
    columns,
    suggestions: suggestions.sort((left, right) => targets.indexOf(left.target) - targets.indexOf(right.target)),
    previewRows: rows.slice(0, 6)
  };
}

function inferCategoricalValue(target, rawValue) {
  const value = String(rawValue || "").trim();
  const normalized = normalizeSampleValue(rawValue).replace(/\s+/g, "");
  if (!value) return null;

  if (target === "treatment") {
    if (/标准治疗|对照组|对照|标准|control|standard/.test(normalized)) return { suggested: "standard", reason: "像标准治疗/对照组" };
    if (/新治疗|试验组|实验组|试验|实验|new|treat/.test(normalized)) return { suggested: "new", reason: "像新治疗/试验组" };
    return { suggested: "", reason: "治疗分组含义待确认" };
  }
  if (target === "event") {
    if (/^(1|死亡|复发|进展|事件|阳性|dead|event|progression|relapse)$/.test(normalized)) return { suggested: "1", reason: "像事件发生" };
    if (/^(0|存活|无事件|失访|删失|阴性|alive|censor|censored|none)$/.test(normalized)) return { suggested: "0", reason: "像删失/未发生事件" };
    return { suggested: "", reason: "事件还是删失需要确认" };
  }
  if (target === "stage") {
    if (/^(iv期|四期|ⅳ|iv|4期|4)$/.test(normalized)) return { suggested: "IV", reason: "像 IV 期" };
    if (/^(iii期|三期|ⅲ|iii|3期|3)$/.test(normalized)) return { suggested: "III", reason: "像 III 期" };
    if (/^(ii期|二期|ⅱ|ii|2期|2)$/.test(normalized)) return { suggested: "II", reason: "像 II 期" };
    if (/^(i期|一期|ⅰ|i|1期|1)$/.test(normalized)) return { suggested: "I", reason: "像 I 期" };
    return { suggested: "", reason: "分期写法不确定" };
  }
  if (target === "sex") {
    if (/^(男|男性|male|m)$/.test(normalized)) return { suggested: "male", reason: "像男性" };
    if (/^(女|女性|female|f)$/.test(normalized)) return { suggested: "female", reason: "像女性" };
    return { suggested: "", reason: "性别取值待确认" };
  }
  return null;
}

function detectValueSuggestions(content, fields) {
  const rows = parseCSVRows(content, 40);
  if (rows.length < 2) return [];
  const headers = rows[0].map((value, index) => String(value || "").trim() || `未命名列${index + 1}`);
  const indexByHeader = new Map(headers.map((header, index) => [header, index]));
  const normalizedIndexByHeader = new Map(headers.map((header, index) => [normalizeColumnName(header), index]));
  const categoricalTargets = ["treatment", "event", "stage", "sex"];
  return fields
    .filter((field) => categoricalTargets.includes(field.target) && String(field.source || "").trim())
    .map((field) => {
      const source = String(field.source || "").trim();
      const columnIndex = indexByHeader.get(source) ?? normalizedIndexByHeader.get(normalizeColumnName(source));
      if (columnIndex === undefined) return null;
      const distinctValues = [];
      const seen = new Set();
      for (const row of rows.slice(1)) {
        const raw = String(row[columnIndex] || "").trim();
        if (!raw || seen.has(raw)) continue;
        seen.add(raw);
        distinctValues.push(raw);
        if (distinctValues.length >= 10) break;
      }
      const suggestions = distinctValues
        .map((raw) => {
          const inferred = inferCategoricalValue(field.target, raw);
          return inferred ? { raw, ...inferred } : null;
        })
        .filter(Boolean);
      if (!suggestions.length) return null;
      return {
        target: field.target,
        source,
        suggestions,
        mappedCount: suggestions.filter((item) => item.suggested).length,
        unresolvedCount: suggestions.filter((item) => !item.suggested).length
      };
    })
    .filter(Boolean);
}

function rColumn(name) {
  const normalized = String(name || "").trim().replaceAll("`", "");
  return `\`${normalized || "未选择列"}\``;
}

function rString(value) {
  return JSON.stringify(String(value || ""));
}

function buildRNamedVector(valueSuggestions, target) {
  const targetSuggestions = (valueSuggestions || [])
    .find((item) => item.target === target)
    ?.suggestions
    ?.filter((item) => item.suggested) || [];
  if (!targetSuggestions.length) return "character()";
  return `c(${targetSuggestions.map((item) => `${rString(item.raw)} = ${rString(item.suggested)}`).join(", ")})`;
}

function buildRStringVector(valueSuggestions, target) {
  const unresolved = (valueSuggestions || [])
    .find((item) => item.target === target)
    ?.suggestions
    ?.filter((item) => !item.suggested)
    ?.map((item) => item.raw) || [];
  if (!unresolved.length) return "character()";
  return `c(${unresolved.map((item) => rString(item)).join(", ")})`;
}

function buildRCharacterVector(values) {
  if (!values.length) return "character()";
  return `c(${values.map((item) => rString(item)).join(", ")})`;
}

function buildDataCleaningRCode(fields, options = {}) {
  const source = (target) => fields.find((field) => field.target === target)?.source || target;
  const encoding = options.encoding === "GBK" ? "GBK" : "UTF-8";
  const valueSuggestions = options.valueSuggestions || [];
  const treatmentValueMap = buildRNamedVector(valueSuggestions, "treatment");
  const eventValueMap = buildRNamedVector(valueSuggestions, "event");
  const stageValueMap = buildRNamedVector(valueSuggestions, "stage");
  const sexValueMap = buildRNamedVector(valueSuggestions, "sex");
  const unresolvedEventValues = buildRStringVector(valueSuggestions, "event");
  const unresolvedTreatmentValues = buildRStringVector(valueSuggestions, "treatment");
  const unresolvedStageValues = buildRStringVector(valueSuggestions, "stage");
  const unresolvedSexValues = buildRStringVector(valueSuggestions, "sex");
  const mappingTargets = buildRCharacterVector(fields.map((field) => field.target));
  const mappingSources = buildRCharacterVector(fields.map((field) => field.source || field.target));
  return `# 规则来源：${R_SURVIVAL_DATA_CLEANING_SKILL_PATH}
# 说明：脚本优先使用当前项目推断出的 value_map，再用轻量兜底规则补足；最终规则仍应由 Skill 和分析目标校正。

raw_path <- "data/raw/随访数据.csv"
raw_path_fallback <- "data/raw/followup.csv"
output_path <- "data/processed/followup_clean.csv"
input_encoding <- Sys.getenv("FOLLOWUP_ENCODING", ${rString(encoding)})

if (!file.exists(raw_path) || file.info(raw_path)$size == 0) {
  raw_path <- raw_path_fallback
}
if (!file.exists(raw_path) || file.info(raw_path)$size == 0) {
  stop(paste("缺少原始数据文件:", raw_path))
}

read_followup_csv <- function(path, encoding) {
  if (identical(encoding, "GBK")) {
    return(read.csv(path, fileEncoding = "GBK", check.names = FALSE, stringsAsFactors = FALSE))
  }
  read.csv(path, check.names = FALSE, stringsAsFactors = FALSE)
}

raw_followup <- read_followup_csv(raw_path, input_encoding)

column <- function(name) {
  if (!name %in% names(raw_followup)) stop(paste("缺少字段:", name))
  raw_followup[[name]]
}

squish <- function(x) trimws(gsub("\\\\s+", " ", as.character(x)))
parse_number <- function(x) suppressWarnings(as.numeric(gsub("[^0-9.+-]", "", as.character(x))))
map_by_dictionary <- function(values, dictionary) {
  normalized <- squish(values)
  if (!length(dictionary)) {
    return(rep(NA_character_, length(normalized)))
  }
  unname(dictionary[normalized])
}
prefer_mapping <- function(primary, fallback) {
  result <- as.character(primary)
  fallback <- as.character(fallback)
  missing <- is.na(result) | result == ""
  result[missing] <- fallback[missing]
  result
}
collect_unresolved <- function(raw_values, cleaned_values, expected_unresolved = character()) {
  pending <- sort(unique(raw_values[(is.na(cleaned_values) | cleaned_values == "") & !is.na(raw_values) & nzchar(raw_values)]))
  pending <- unique(c(expected_unresolved, pending))
  pending[nzchar(pending)]
}
cat_md <- function(...) cat(..., sep = "")
md_section <- function(title) cat_md("## ", title, "\\n\\n")
md_bullet <- function(label, value) cat_md("- ", label, "：", value, "\\n")
md_values <- function(values) {
  if (!length(values)) return("无")
  paste(sprintf("\`%s\`", values), collapse = "、")
}

treatment_value_map <- ${treatmentValueMap}
event_value_map <- ${eventValueMap}
stage_value_map <- ${stageValueMap}
sex_value_map <- ${sexValueMap}
event_unresolved_seed <- ${unresolvedEventValues}
treatment_unresolved_seed <- ${unresolvedTreatmentValues}
stage_unresolved_seed <- ${unresolvedStageValues}
sex_unresolved_seed <- ${unresolvedSexValues}
mapping_targets <- ${mappingTargets}
mapping_sources <- ${mappingSources}

patient_id_raw <- as.character(column(${rString(source("patient_id"))}))
treatment_raw <- squish(column(${rString(source("treatment"))}))
event_raw <- squish(column(${rString(source("event"))}))
stage_raw <- squish(column(${rString(source("stage"))}))
sex_raw <- squish(column(${rString(source("sex"))}))
treatment_exact <- map_by_dictionary(treatment_raw, treatment_value_map)
event_exact <- map_by_dictionary(event_raw, event_value_map)
stage_exact <- map_by_dictionary(stage_raw, stage_value_map)
sex_exact <- map_by_dictionary(sex_raw, sex_value_map)

treatment_fallback <- ifelse(grepl("新|试验", treatment_raw), "new", ifelse(grepl("标准|对照", treatment_raw), "standard", NA))
event_fallback <- ifelse(event_raw %in% c("死亡", "复发", "进展", "1"), "1", ifelse(event_raw %in% c("存活", "无事件", "失访", "0"), "0", NA))
stage_fallback <- ifelse(grepl("IV期|四期|Ⅳ", stage_raw), "IV",
  ifelse(grepl("III期|三期|Ⅲ", stage_raw), "III",
    ifelse(grepl("II期|二期|Ⅱ", stage_raw), "II",
      ifelse(grepl("I期|一期|Ⅰ", stage_raw), "I", NA))))
sex_fallback <- ifelse(grepl("男", sex_raw), "male", ifelse(grepl("女", sex_raw), "female", NA))

treatment_clean <- prefer_mapping(treatment_exact, treatment_fallback)
event_clean <- suppressWarnings(as.integer(prefer_mapping(event_exact, event_fallback)))
stage_clean <- prefer_mapping(stage_exact, stage_fallback)
sex_clean <- prefer_mapping(sex_exact, sex_fallback)

raw_row_count <- nrow(raw_followup)
followup <- data.frame(
  patient_id = patient_id_raw,
  treatment = treatment_clean,
  followup_month = parse_number(column(${rString(source("followup_month"))})),
  event = event_clean,
  age = parse_number(column(${rString(source("age"))})),
  stage = stage_clean,
  sex = sex_clean,
  stringsAsFactors = FALSE
)

duplicate_removed <- sum(duplicated(followup$patient_id))
followup <- followup[!duplicated(followup$patient_id), ]

missing_patient_id <- is.na(followup$patient_id) | !nzchar(trimws(followup$patient_id))
missing_followup <- is.na(followup$followup_month)
missing_event <- is.na(followup$event)
negative_followup <- !is.na(followup$followup_month) & followup$followup_month < 0
invalid_mask <- missing_patient_id | missing_followup | missing_event | negative_followup
invalid_removed <- sum(invalid_mask)
followup <- followup[!invalid_mask, ]

event_unresolved <- collect_unresolved(event_raw, as.character(event_clean), event_unresolved_seed)
treatment_unresolved <- collect_unresolved(treatment_raw, treatment_clean, treatment_unresolved_seed)
stage_unresolved <- collect_unresolved(stage_raw, stage_clean, stage_unresolved_seed)
sex_unresolved <- collect_unresolved(sex_raw, sex_clean, sex_unresolved_seed)

dir.create("data/processed", recursive = TRUE, showWarnings = FALSE)
write.csv(followup, output_path, row.names = FALSE, fileEncoding = "UTF-8")

cat_md("# 数据清洗执行报告\\n\\n")
md_section("执行结果")
md_bullet("原始行数", raw_row_count)
md_bullet("输出行数", nrow(followup))
md_bullet("重复编号移除", duplicate_removed)
md_bullet("无效记录移除", invalid_removed)
md_bullet("输出文件", sprintf("\`%s\`", output_path))
cat_md("\\n")

md_section("字段映射")
for (index in seq_along(mapping_targets)) {
  cat_md("- \`", mapping_targets[[index]], "\` <- \`", mapping_sources[[index]], "\`\\n")
}
cat_md("\\n")

md_section("质量检查")
md_bullet("patient_id_unique", if (anyDuplicated(followup$patient_id) == 0) "通过" else "失败")
md_bullet("followup_month_non_negative", if (all(followup$followup_month >= 0, na.rm = TRUE)) "通过" else "失败")
md_bullet("event_binary_0_1", if (all(followup$event %in% c(0L, 1L), na.rm = TRUE)) "通过" else "失败")
md_bullet("required_survival_fields_not_missing", if (all(!is.na(followup$patient_id) & nzchar(followup$patient_id) & !is.na(followup$followup_month) & !is.na(followup$event))) "通过" else "失败")
md_bullet("缺失 patient_id", sum(missing_patient_id))
md_bullet("缺失 followup_month", sum(missing_followup))
md_bullet("缺失 event", sum(missing_event))
md_bullet("负数随访时间", sum(negative_followup))
cat_md("\\n")

md_section("待确认取值")
cat_md("### 结局\\n\\n", md_values(event_unresolved), "\\n\\n")
cat_md("### 治疗组\\n\\n", md_values(treatment_unresolved), "\\n\\n")
cat_md("### 分期\\n\\n", md_values(stage_unresolved), "\\n\\n")
cat_md("### 性别\\n\\n", md_values(sex_unresolved), "\\n\\n")

md_section("数据概览")
summary_text <- paste(capture.output(summary(followup)), collapse = "\\n")
cat_md("\`\`\`text\\n", summary_text, "\\n\`\`\`\\n")`;
}

function buildVariableMappingYAML(fields, options = {}) {
  const encoding = options.encoding === "GBK" ? "GBK" : "UTF-8";
  const valueSuggestions = new Map((options.valueSuggestions || []).map((item) => [item.target, item]));
  const lines = [
    "version: 1",
    "rule_source:",
    `  skill: ${R_SURVIVAL_DATA_CLEANING_SKILL_PATH}`,
    "  mode: agent_inferred_with_human_confirmation",
    "dataset:",
    "  raw_path: data/raw/随访数据.csv",
    `  encoding: ${encoding}`,
    "  output_object: followup",
    "variables:"
  ];
  for (const field of fields) {
    const valueSuggestion = valueSuggestions.get(field.target);
    const mappedSuggestions = valueSuggestion?.suggestions.filter((item) => item.suggested) || [];
    const unresolvedSuggestions = valueSuggestion?.suggestions.filter((item) => !item.suggested).map((item) => item.raw) || [];
    lines.push(
      `  ${field.target}:`,
      `    source: ${JSON.stringify(field.source || "")}`,
      `    type: ${JSON.stringify(field.type || "")}`,
      `    semantic_role: ${JSON.stringify(field.semanticRole || "")}`,
      `    transform_guidance: ${JSON.stringify(field.hint || "")}`
    );
    if (mappedSuggestions.length) {
      lines.push("    value_map:");
      for (const suggestion of mappedSuggestions) {
        lines.push(`      ${JSON.stringify(suggestion.raw)}: ${JSON.stringify(suggestion.suggested)}`);
      }
    } else {
      lines.push("    value_map: {}");
    }
    if (unresolvedSuggestions.length) {
      lines.push("    unresolved_values:");
      for (const value of unresolvedSuggestions) {
        lines.push(`      - ${JSON.stringify(value)}`);
      }
    } else {
      lines.push("    unresolved_values: []");
    }
  }
  lines.push(
    "quality_checks:",
    "  required:",
    "    - patient_id_unique",
    "    - followup_month_non_negative",
    "    - event_binary_0_1",
    "    - required_survival_fields_not_missing",
    "  agent_should_expand: true",
    "confirmation_required:",
    "  - event_definition",
    "  - censoring_definition",
    "  - ambiguous_value_maps"
  );
  return `${lines.join("\n")}\n`;
}

function parseMarkdownBulletSection(text, title) {
  const section = extractMarkdownSection(text, title);
  if (!section) return [];
  return section
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.startsWith("- "))
    .map((line) => line.slice(2).trim());
}

function extractMarkdownSection(text, title) {
  const value = String(text || "");
  const escaped = title.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const pattern = new RegExp(`## ${escaped}\\n\\n([\\s\\S]*?)(?=\\n## |$)`);
  return value.match(pattern)?.[1]?.trim() || "";
}

function parsePendingValueSection(text) {
  const section = extractMarkdownSection(text, "待确认取值");
  if (!section) return [];
  const pattern = /###\s+([^\n]+)\n\n([\s\S]*?)(?=\n###\s+|$)/g;
  const items = [];
  let match;
  while ((match = pattern.exec(section))) {
    const label = String(match[1] || "").trim();
    const values = String(match[2] || "").trim();
    if (!label || !values || values === "无") continue;
    items.push(`${label}：${values}`);
  }
  return items;
}

function summarizeCleaningReport(reportText) {
  const report = String(reportText || "").trim();
  if (!report) {
    return {
      available: false,
      execution: [],
      qualityChecks: [],
      pendingValues: [],
      runtimeMeta: [],
      sourceText: ""
    };
  }
  return {
    available: true,
    execution: parseMarkdownBulletSection(report, "执行结果"),
    qualityChecks: parseMarkdownBulletSection(report, "质量检查"),
    pendingValues: parsePendingValueSection(report),
    runtimeMeta: parseMarkdownBulletSection(report, "运行元数据"),
    sourceText: report
  };
}

function formatCleaningReportContext(reportSummary) {
  if (!reportSummary?.available) return "暂无最新数据清洗报告";
  const sections = [];
  if (reportSummary.execution.length) {
    sections.push([
      "[最近一次清洗执行结果]",
      ...reportSummary.execution.map((item) => `- ${item}`)
    ].join("\n"));
  }
  if (reportSummary.qualityChecks.length) {
    sections.push([
      "[最近一次质量检查]",
      ...reportSummary.qualityChecks.map((item) => `- ${item}`)
    ].join("\n"));
  }
  if (reportSummary.pendingValues.length) {
    sections.push([
      "[最近一次待确认取值]",
      ...reportSummary.pendingValues.map((item) => `- ${item}`)
    ].join("\n"));
  }
  if (reportSummary.runtimeMeta.length) {
    sections.push([
      "[最近一次运行元数据]",
      ...reportSummary.runtimeMeta.map((item) => `- ${item}`)
    ].join("\n"));
  }
  sections.push("[最近一次完整报告]\n```markdown\n" + reportSummary.sourceText + "\n```");
  return sections.join("\n\n");
}

function reportValueForPrefix(items, prefix) {
  const match = (items || []).find((item) => item.startsWith(`${prefix}：`));
  return match ? match.slice(prefix.length + 1).trim() : "";
}

function survivalCleaningSkillContent(project) {
  return projectFile(project, R_SURVIVAL_DATA_CLEANING_SKILL_PATH)?.content || R_SURVIVAL_DATA_CLEANING_SKILL_CONTENT;
}

function buildSurvivalCleaningAgentPrompt({ project, fields, encoding, mappingYAML, code, request, valueSuggestions = [], reportSummary }) {
  const mapping = fields.map((field) => [
    `- ${field.source || "未填写"} -> ${field.target}`,
    `  类型：${field.type}`,
    `  语义：${field.semanticRole}`,
    `  提示：${field.hint}`
  ].join("\n")).join("\n");
  const valueSuggestionText = valueSuggestions.length
    ? valueSuggestions.map((item) => [
      `- ${item.target} <- ${item.source}`,
      ...item.suggestions.map((suggestion) => `  - ${suggestion.raw} -> ${suggestion.suggested || "待确认"} (${suggestion.reason})`)
    ].join("\n")).join("\n")
    : "暂无自动值映射建议";
  return [
    "请按项目内 Skill 执行，不要把当前默认映射当成固定规则。",
    "",
    `[Skill 文件] ${R_SURVIVAL_DATA_CLEANING_SKILL_PATH}`,
    "```markdown",
    survivalCleaningSkillContent(project),
    "```",
    "",
    "[当前项目]",
    `项目：${project?.name || ""}`,
    `目标：${project?.objective || "未填写"}`,
    `文件编码：${encoding}`,
    "",
    "[字段映射草案]",
    mapping,
    "",
    "[样本值映射建议]",
    valueSuggestionText,
    "",
    "[最近一次数据清洗报告]",
    formatCleaningReportContext(reportSummary),
    "",
    "[variable-mapping.yml 草案]",
    "```yaml",
    mappingYAML,
    "```",
    "",
    "[当前 R 清洗脚本草案]",
    "```r",
    code,
    "```",
    "",
    "[请输出]",
    "1. 需要向用户确认的问题",
    "2. 如需更新映射配置，请严格使用以下格式：",
    "### config/variable-mapping.yml",
    "```yaml",
    "# 完整文件内容",
    "```",
    "3. 如需更新清洗脚本，请严格使用以下格式：",
    "### R/clean-data.R",
    "```r",
    "# 完整文件内容",
    "```",
    "4. 清洗后应生成的质量检查摘要",
    "",
    `[用户请求] ${request || "请生成下一版清洗方案"}`
  ].join("\n");
}

function suggestedFilePathFromFence(info, before, content) {
  const context = `${info || ""}\n${before || ""}`;
  const explicit = context.match(/(?:path|file)\s*=\s*["']?(config\/variable-mapping\.ya?ml|R\/clean-data\.R)["']?/i);
  if (explicit) return explicit[1].replace(/yaml$/i, "yml").replace(/^r\//i, "R/");
  if (/config\/variable-mapping\.ya?ml/i.test(context)) return "config/variable-mapping.yml";
  if (/R\/clean-data\.R/i.test(context)) return "R/clean-data.R";
  const language = String(info || "").trim().toLowerCase();
  if (/^ya?ml\b/.test(language) && /rule_source:|variables:|quality_checks:/.test(content)) return "config/variable-mapping.yml";
  if (/^r\b/.test(language) && /library\(|<-|clean_followup|followup/.test(content)) return "R/clean-data.R";
  return "";
}

function extractSuggestedProjectFiles(text) {
  const value = String(text || "");
  const files = [];
  const seen = new Set();
  const pattern = /```([^\n`]*)\n([\s\S]*?)```/g;
  let match;
  while ((match = pattern.exec(value))) {
    const info = match[1] || "";
    const content = (match[2] || "").trim();
    if (!content) continue;
    const before = value.slice(Math.max(0, match.index - 260), match.index);
    const path = suggestedFilePathFromFence(info, before, content);
    if (!path || seen.has(path)) continue;
    seen.add(path);
    files.push({ path, content });
  }
  return files;
}

function splitLinesForDiff(value) {
  return String(value || "").replace(/\r\n/g, "\n").replace(/\r/g, "\n").split("\n");
}

function buildSuggestedFileDiff(currentContent, nextContent) {
  const currentLines = splitLinesForDiff(currentContent);
  const nextLines = splitLinesForDiff(nextContent);
  let prefix = 0;
  while (
    prefix < currentLines.length &&
    prefix < nextLines.length &&
    currentLines[prefix] === nextLines[prefix]
  ) {
    prefix += 1;
  }
  let suffix = 0;
  while (
    suffix < currentLines.length - prefix &&
    suffix < nextLines.length - prefix &&
    currentLines[currentLines.length - 1 - suffix] === nextLines[nextLines.length - 1 - suffix]
  ) {
    suffix += 1;
  }
  const currentChanged = currentLines.slice(prefix, currentLines.length - suffix);
  const nextChanged = nextLines.slice(prefix, nextLines.length - suffix);
  return {
    changed: currentChanged.length > 0 || nextChanged.length > 0,
    startLine: prefix + 1,
    currentChanged,
    nextChanged
  };
}

function snapshotProjectForUndo(project, label) {
  if (!project) return null;
  return {
    label: String(label || "上一步修改"),
    createdAt: new Date().toISOString(),
    projectID: project.id,
    activeFile: project.activeFile || "",
    notebookCode: project.notebookCode || "",
    files: clone(project.files || [])
  };
}

function formatUndoTimestamp(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function repositoryFor(scope) {
  const key = `${scope.workspaceId}:${scope.userId}`;
  if (!repositories.has(key)) {
    repositories.set(key, createAnalysisWorkspaceRepository({ storage: window.localStorage, scope }));
  }
  return repositories.get(key);
}

function backendClientFor(context) {
  return createAnalysisWorkspaceClient({ http: context.http, scope: context.scope });
}

function localProjects(repository) {
  return repository.list().map((project) => ({ ...project, persistence: "local" }));
}

function projectForm() {
  return {
    title: "新建分析项目",
    description: "创建后端项目；已配置 GitLab Connector 时会自动建立私有仓库并提交分析模板。",
    schema: {
      type: "object",
      required: ["name"],
      properties: {
        name: { type: "string", title: "项目名称", description: "例如：肿瘤患者生存分析" },
        objective: { type: "string", format: "textarea", title: "分析目标" },
        repositoryPath: { type: "string", title: "GitLab 项目路径", description: "例如：survival-analysis" },
        notebookURL: { type: "string", title: "JupyterLab 地址", description: "开发环境可使用 http://127.0.0.1:18888/lab" }
      }
    },
    initialValues: {},
    submitLabel: "创建项目"
  };
}

function settingsForm(project) {
  return {
    title: "项目连接设置",
    description: project.name,
    schema: {
      type: "object",
      properties: {
        notebookURL: { type: "string", title: "JupyterLab 地址" }
      }
    },
    initialValues: {
      notebookURL: project.notebookURL || ""
    },
    submitLabel: "保存连接"
  };
}

function statusLabel(project) {
  if (project.persistence === "local") return "本地示例";
  if (project.gitStatus === "synced") return "GitLab 已同步";
  if (project.gitStatus === "syncing") return "正在同步";
  if (project.gitStatus === "error") return "同步失败";
  return "GitLab 待配置";
}

function runtimeLabel(project) {
  switch (project.runtimeStatus) {
    case "running":
      return "R Runtime 已运行";
    case "starting":
      return "R Runtime 启动中";
    case "stopped":
      return "R Runtime 已停止";
    case "error":
      return "R Runtime 启动失败";
    default:
      return project.notebookURL ? "R Runtime 已配置" : "R Runtime 待连接";
  }
}

function fileIcon(path, kind) {
  if (kind === "folder") return <Folder aria-hidden="true" />;
  if (path.endsWith(".ipynb")) return <FileCode2 aria-hidden="true" />;
  if (path.endsWith(".R")) return <Code2 aria-hidden="true" />;
  if (path.endsWith(".yml") || path.endsWith(".yaml")) return <Settings2 aria-hidden="true" />;
  if (path.endsWith(".md")) return <FileText aria-hidden="true" />;
  if (path.endsWith(".xlsx") || path.endsWith(".csv")) return <Sheet aria-hidden="true" />;
  return <File aria-hidden="true" />;
}

function basename(path) {
  return String(path || "").split("/").filter(Boolean).at(-1) || path;
}

function projectFile(project, path) {
  return project?.files?.find((file) => file.path === path && file.kind === "file") || null;
}

function displayCodeForProject(project) {
  if (!project) return "";
  const file = projectFile(project, project.activeFile);
  if (file?.content) return file.content;
  return project.notebookCode || "";
}

function withProjectFileContent(files, path, content) {
  const normalizedPath = String(path || "").trim();
  let updated = false;
  const next = (Array.isArray(files) ? files : []).map((file) => {
    if (file.path !== normalizedPath || file.kind !== "file") return file;
    updated = true;
    return { ...file, content, status: "modified" };
  });
  if (!updated && normalizedPath) {
    const folders = normalizedPath.split("/").slice(0, -1);
    let prefix = "";
    for (const folder of folders) {
      prefix = prefix ? `${prefix}/${folder}` : folder;
      if (!next.some((file) => file.path === prefix)) next.push({ path: prefix, kind: "folder" });
    }
    next.push({ path: normalizedPath, kind: "file", status: "modified", content });
  }
  return next;
}

function languageForPath(path) {
  if (path.endsWith(".R")) return "R";
  if (path.endsWith(".yml") || path.endsWith(".yaml")) return "YAML";
  if (path.endsWith(".md")) return "Markdown";
  if (path.endsWith(".json") || path.endsWith(".ipynb")) return "JSON";
  return "Text";
}

function selectedFileTitle(path) {
  return path || "未选择文件";
}

function eventText(event) {
  const payload = event?.payload || {};
  if (Array.isArray(payload.content)) {
    return payload.content.map((item) => item?.text || item?.content || "").filter(Boolean).join("\n");
  }
  if (typeof payload.content === "string") return payload.content;
  return payload.message || payload.summary || payload.text || "";
}

function wait(milliseconds, signal) {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(resolve, milliseconds);
    signal?.addEventListener("abort", () => {
      window.clearTimeout(timer);
      reject(new DOMException("Aborted", "AbortError"));
    }, { once: true });
  });
}

async function waitForAgentReply(context, sessionID, runID, signal) {
  const session = encodeURIComponent(sessionID);
  const run = encodeURIComponent(runID);
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const [runState, eventList] = await Promise.all([
      context.http.request(`/v2/sessions/${session}/runs/${run}`, { signal }),
      context.http.request(`/v2/sessions/${session}/runs/${run}/events`, { signal })
    ]);
    const events = Array.isArray(eventList?.events) ? eventList.events : [];
    const reply = [...events].reverse().find((event) => event.type === "agent.message" && eventText(event).trim());
    if (reply) return eventText(reply).trim();
    if (["failed", "interrupted"].includes(runState?.status)) {
      throw new Error(runState.error_message || `Agent Run ${runState.status}`);
    }
    if (runState?.status === "completed") return "任务已完成，未返回可见文本。";
    await wait(1500, signal);
  }
  return "任务仍在运行，可在主工作台继续查看。";
}

function KMSurvivalChart() {
  return (
    <svg className="analysis-km-chart" viewBox="0 0 620 300" role="img" aria-labelledby="analysis-km-title analysis-km-desc">
      <title id="analysis-km-title">Kaplan-Meier 生存曲线示例输出</title>
      <desc id="analysis-km-desc">新治疗组的生存概率整体高于标准治疗组。</desc>
      {[50, 100, 150, 200, 250].map((y) => <line className="grid" x1="58" x2="596" y1={y} y2={y} key={y} />)}
      <line className="axis" x1="58" x2="596" y1="250" y2="250" />
      <line className="axis" x1="58" x2="58" y1="36" y2="250" />
      <text className="axis-label" x="8" y="22">生存概率</text>
      <text className="axis-label" x="314" y="290" textAnchor="middle">随访时间（月）</text>
      <text className="tick" x="48" y="254" textAnchor="end">0%</text>
      <text className="tick" x="48" y="204" textAnchor="end">25%</text>
      <text className="tick" x="48" y="154" textAnchor="end">50%</text>
      <text className="tick" x="48" y="104" textAnchor="end">75%</text>
      <text className="tick" x="48" y="54" textAnchor="end">100%</text>
      {[58, 192, 326, 460, 596].map((x, index) => <text className="tick" x={x} y="270" textAnchor="middle" key={x}>{index * 6}</text>)}
      <path className="curve primary" d="M58 50 H100 V57 H145 V66 H190 V77 H235 V91 H280 V106 H325 V120 H370 V140 H415 V158 H460 V178 H505 V194 H550 V211 H596" />
      <path className="curve secondary" d="M58 50 H95 V63 H130 V78 H170 V96 H210 V117 H250 V140 H290 V162 H330 V181 H375 V202 H420 V216 H470 V229 H520 V239 H560 V245 H596" />
      <text className="annotation" x="414" y="74">HR 0.68 · p = 0.021</text>
      <g className="legend" transform="translate(408 92)">
        <line className="curve primary" x1="0" x2="22" y1="0" y2="0" /><text x="30" y="4">新治疗组</text>
        <line className="curve secondary" x1="0" x2="22" y1="22" y2="22" /><text x="30" y="26">标准治疗组</text>
      </g>
    </svg>
  );
}

function NotebookPreview({ code, onCodeChange, onCodeSave, onOpenRuntime, runtimeAvailable }) {
  return (
    <div className="analysis-notebook" aria-label="Notebook 预览">
      <article className="analysis-notebook-cell markdown-cell">
        <div className="cell-gutter">MD</div>
        <div className="cell-content">
          <h2>治疗组总生存期比较</h2>
          <p>使用 Kaplan-Meier 方法估计生存函数，并通过 Cox 比例风险模型调整年龄和疾病分期。</p>
        </div>
      </article>

      <article className="analysis-notebook-cell code-cell">
        <div className="cell-gutter">R</div>
        <div className="cell-content">
          <div className="cell-toolbar">
            <span>In [1]</span>
            <button className="analysis-icon-button" type="button" disabled={!runtimeAvailable} onClick={onOpenRuntime} title={runtimeAvailable ? "在远程 JupyterLab 中运行" : "先配置远程 JupyterLab"} aria-label="运行代码单元">
              <Play aria-hidden="true" />
            </button>
          </div>
          <textarea className="analysis-code-editor" spellCheck="false" value={code} onChange={(event) => onCodeChange(event.target.value)} onBlur={onCodeSave} aria-label="R 代码" />
        </div>
      </article>

      <article className="analysis-notebook-cell output-cell">
        <div className="cell-gutter">Out</div>
        <div className="cell-content">
          <div className="analysis-output-heading">
            <span>已保存的示例输出</span>
            <span className="analysis-status neutral">未重新运行</span>
          </div>
          <KMSurvivalChart />
        </div>
      </article>

      <article className="analysis-notebook-cell output-cell compact-output">
        <div className="cell-gutter">Out</div>
        <div className="cell-content">
          <table className="analysis-model-table">
            <thead><tr><th>变量</th><th>HR</th><th>95% CI</th><th>p 值</th></tr></thead>
            <tbody>
              <tr><td>新治疗组</td><td>0.68</td><td>0.49–0.94</td><td>0.021</td></tr>
              <tr><td>年龄（每 10 岁）</td><td>1.14</td><td>0.98–1.32</td><td>0.087</td></tr>
              <tr><td>III 期 vs II 期</td><td>1.72</td><td>1.23–2.41</td><td>0.002</td></tr>
            </tbody>
          </table>
        </div>
      </article>
    </div>
  );
}

function FileEditor({ code, dirty, highlighted, path, saving, onAskAI, onCodeChange, onCodeSave, onOpenRuntime, runtimeAvailable }) {
  const language = languageForPath(path);
  return (
    <div className="analysis-file-editor" aria-label="文件编辑器">
      <section className={`analysis-file-editor-card ${highlighted ? "spotlight" : ""}`}>
        <header className="analysis-file-editor-header">
          <div>
            <span className="analysis-status neutral"><FileText aria-hidden="true" />{language}</span>
            <h2>{selectedFileTitle(path)}</h2>
            <p>直接修改当前项目文件；保存后会进入项目文件内容，Runtime / GitLab 同步会使用这一版。</p>
          </div>
          <div className="analysis-file-editor-actions">
            <span className={`analysis-status ${dirty ? "syncing" : "neutral"}`}>{dirty ? "未保存" : "已保存"}</span>
            <button className="secondary" type="button" onClick={onAskAI}><Sparkles aria-hidden="true" />让助手检查</button>
            <button className="secondary" type="button" disabled={!runtimeAvailable} onClick={onOpenRuntime}><Play aria-hidden="true" />去 JupyterLab 运行</button>
            <button type="button" disabled={!dirty || saving} onClick={onCodeSave}>
              {saving ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
              保存文件
            </button>
          </div>
        </header>
        <textarea
          className="analysis-file-code-editor"
          spellCheck="false"
          value={code}
          onChange={(event) => onCodeChange(event.target.value)}
          aria-label={`${path} 文件内容`}
        />
      </section>
    </div>
  );
}

function RuntimeFrame({ project }) {
  if (!project.notebookURL) {
    return (
      <div className="analysis-runtime-empty">
        <Server aria-hidden="true" />
        <strong>{project.runtimeStatus === "stopped" ? "R Runtime 已停止" : "远程 JupyterLab 未连接"}</strong>
        <span>{project.runtimeStatus === "stopped" ? "可以重新启动远程运行环境，继续执行 Notebook。" : "项目设置中配置同源代理地址，或使用开发环境地址。"}</span>
        <code>{project.runtimeStatus === "stopped" ? "重新点击启动 Runtime" : "http://127.0.0.1:18888/lab"}</code>
      </div>
    );
  }
  return (
    <iframe
      className="analysis-runtime-frame"
      src={project.notebookURL}
      title={`${project.name} JupyterLab`}
      sandbox="allow-same-origin allow-scripts allow-forms allow-downloads allow-popups"
    />
  );
}

function DataCleaningWorkbench({
  detectedColumns,
  detectedSuggestions,
  encoding,
  fields,
  generatedCode,
  importingRawData,
  onApplyDetectedMapping,
  onApplyMapping,
  onApplySkill,
  onApplyTemplate,
  onAskAI,
  onEncodingChange,
  onFieldSourceChange,
  onImportError,
  onImportRawData,
  onDraftFailedChecks,
  onDraftPendingValues,
  onInspectCleaningImpact,
  onInspectFailedChecks,
  onInspectPendingValues,
  onInspectSummary,
  onWriteSampleData,
  onRunCleaning,
  rawDataFile,
  runningCleaning,
  savingSampleData,
  saving,
  savingMapping,
  savingSkill,
  skillInstalled,
  valueSuggestions,
  cleaningReportSummary
}) {
  const uploadInputRef = useRef(null);
  const [excelImportDraft, setExcelImportDraft] = useState(null);
  const [pastedRawData, setPastedRawData] = useState("");
  const rawDataLines = rawDataFile?.content ? rawDataFile.content.trim().split(/\r?\n/).length : 0;
  const detectedCount = detectedSuggestions.length;
  const pendingCount = cleaningReportSummary?.pendingValues?.length || 0;
  const failedChecks = (cleaningReportSummary?.qualityChecks || []).filter((item) => /失败$/.test(item));
  const outputRows = reportValueForPrefix(cleaningReportSummary?.execution, "输出行数");
  const invalidRemoved = reportValueForPrefix(cleaningReportSummary?.execution, "无效记录移除");
  const duplicateRemoved = reportValueForPrefix(cleaningReportSummary?.execution, "重复编号移除");
  const excelPreviewRows = excelImportDraft?.previewRows || [];
  const excelPreviewColumnCount = Math.max(1, ...excelPreviewRows.map((row) => row.length));
  const changeExcelSheet = (sheetName) => {
    setExcelImportDraft((draft) => {
      if (!draft) return draft;
      return {
        ...draft,
        selectedSheet: sheetName,
        previewRows: getExcelSheetPreview(draft.xlsx, draft.workbook, sheetName)
      };
    });
  };
  const confirmExcelImport = async () => {
    try {
      const imported = buildExcelSheetImport(excelImportDraft);
      if (!imported.content.trim()) {
        throw new Error("当前工作表没有可导入的数据");
      }
      await onImportRawData(imported.content, imported.sourceName);
      setExcelImportDraft(null);
    } catch (error) {
      onImportError(error);
    }
  };
  return (
    <div className="analysis-cleaning-workbench" aria-label="数据清洗与中文字段映射">
      <section className="analysis-cleaning-hero">
        <div>
          <span className="analysis-status neutral"><Sheet aria-hidden="true" />CSV / Excel 中文随访表</span>
          <h2>先把中文业务字段整理成生存分析可运行的数据集</h2>
          <p>目标输出是 <code>followup</code> 数据框；具体事件、删失、分组和异常值规则交给项目 Skill 与智能体共同生成。</p>
        </div>
        <div className="analysis-cleaning-actions">
          <label className="analysis-cleaning-encoding">
            <span>文件编码</span>
            <select value={encoding} onChange={(event) => onEncodingChange(event.target.value)} aria-label="原始数据文件编码">
              <option value="UTF-8">UTF-8</option>
              <option value="GBK">GBK / 中文 Windows</option>
            </select>
          </label>
          <button className="secondary" type="button" onClick={onAskAI}><Sparkles aria-hidden="true" />按 Skill 生成方案</button>
          <button className="secondary" type="button" disabled={savingSkill} onClick={onApplySkill}>
            {savingSkill ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <FileText aria-hidden="true" />}
            {skillInstalled ? "刷新 Skill" : "写入 Skill"}
          </button>
          <button className="secondary" type="button" disabled={savingMapping} onClick={onApplyMapping}>
            {savingMapping ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Settings2 aria-hidden="true" />}
            写入映射配置
          </button>
          <button className="secondary" type="button" disabled={savingSampleData} onClick={onWriteSampleData}>
            {savingSampleData ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Sheet aria-hidden="true" />}
            写入示例数据
          </button>
          <button type="button" disabled={saving} onClick={onApplyTemplate}>
            {saving ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Code2 aria-hidden="true" />}
            写入 R/clean-data.R
          </button>
          <button type="button" disabled={runningCleaning} onClick={onRunCleaning}>
            {runningCleaning ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Play aria-hidden="true" />}
            运行清洗流程
          </button>
        </div>
      </section>

      {cleaningReportSummary?.available ? (
        <section className="analysis-cleaning-report-summary">
          <article className="analysis-cleaning-summary-card highlight">
            <span className="analysis-cleaning-summary-label">最新清洗结果</span>
            <strong>{outputRows ? `${outputRows} 行可用于分析` : "已生成最新报告"}</strong>
            <p>{failedChecks.length ? `有 ${failedChecks.length} 项检查未通过` : "当前质量检查没有失败项"}</p>
            <button className="secondary analysis-cleaning-summary-action" type="button" onClick={onInspectSummary}>
              <Sparkles aria-hidden="true" />
              让助手解读
            </button>
          </article>
          <article className="analysis-cleaning-summary-card">
            <span className="analysis-cleaning-summary-label">记录处理</span>
            <strong>{duplicateRemoved || "0"} 条重复编号</strong>
            <p>{invalidRemoved || "0"} 条记录因关键缺失或非法时间被移除</p>
            <button className="secondary analysis-cleaning-summary-action" type="button" onClick={onInspectCleaningImpact}>
              <Sparkles aria-hidden="true" />
              分析清洗影响
            </button>
          </article>
          <article className="analysis-cleaning-summary-card warn">
            <span className="analysis-cleaning-summary-label">待确认值</span>
            <strong>{pendingCount} 类待确认项</strong>
            <p>{pendingCount ? cleaningReportSummary.pendingValues.slice(0, 2).join("；") : "当前没有待确认取值"}</p>
            <button className="secondary analysis-cleaning-summary-action" type="button" disabled={!pendingCount} onClick={onInspectPendingValues}>
              <Sparkles aria-hidden="true" />
              处理待确认值
            </button>
          </article>
        </section>
      ) : null}

      {cleaningReportSummary?.available && (failedChecks.length || pendingCount) ? (
        <section className="analysis-cleaning-card analysis-cleaning-report-alerts">
          <div className="analysis-cleaning-card-title-inline">
            <h3>0. 最新报告关注点</h3>
            <span>来自最近一次清洗执行</span>
          </div>
          {failedChecks.length ? (
            <div className="analysis-cleaning-alert-group">
              <strong>失败的质量检查</strong>
              <ul>
                {failedChecks.map((item) => <li key={item}>{item}</li>)}
              </ul>
              <div className="analysis-cleaning-alert-actions">
                <button className="secondary analysis-cleaning-alert-action" type="button" onClick={onInspectFailedChecks}>
                  <Sparkles aria-hidden="true" />
                  让助手给修复建议
                </button>
                <button className="secondary analysis-cleaning-alert-action" type="button" onClick={onDraftFailedChecks}>
                  <Code2 aria-hidden="true" />
                  生成脚本修订
                </button>
              </div>
            </div>
          ) : null}
          {pendingCount ? (
            <div className="analysis-cleaning-alert-group">
              <strong>待确认取值</strong>
              <ul>
                {cleaningReportSummary.pendingValues.map((item) => <li key={item}>{item}</li>)}
              </ul>
              <div className="analysis-cleaning-alert-actions">
                <button className="secondary analysis-cleaning-alert-action" type="button" onClick={onInspectPendingValues}>
                  <Sparkles aria-hidden="true" />
                  生成处理建议
                </button>
                <button className="secondary analysis-cleaning-alert-action" type="button" onClick={onDraftPendingValues}>
                  <Settings2 aria-hidden="true" />
                  生成映射草案
                </button>
              </div>
            </div>
          ) : null}
        </section>
      ) : null}

      <section className="analysis-cleaning-grid">
        <article className="analysis-cleaning-card analysis-cleaning-skill-card">
          <div>
            <h3>规则来源：项目 Skill</h3>
            <p>把通用方法论放在 <code>{R_SURVIVAL_DATA_CLEANING_SKILL_PATH}</code>，让智能体根据原始数据样本和研究目标生成具体规则。</p>
          </div>
          <span className={`analysis-status ${skillInstalled ? "synced" : "neutral"}`}>
            {skillInstalled ? "项目已包含" : "当前项目待写入"}
          </span>
        </article>

        <article className="analysis-cleaning-card">
          <h3>1. 导入原始数据</h3>
          <p>支持中文列名、CSV/TXT 和 Excel；Excel 会先选择工作表并预览，再保留为原始表，后续生成标准分析表。</p>
          <div className="analysis-cleaning-dropzone">
            <FileText aria-hidden="true" />
            <strong>data/raw/随访数据.csv</strong>
            <span>{rawDataFile?.content ? `已导入 ${rawDataLines} 行，可继续运行清洗流程` : "可上传 CSV/Excel、粘贴表格文本，或先写入示例数据跑通流程"}</span>
          </div>
          <input
            ref={uploadInputRef}
            type="file"
            accept=".csv,.txt,.xlsx,.xls,text/csv,text/plain,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,application/vnd.ms-excel"
            hidden
            onChange={async (event) => {
              const file = event.target.files?.[0];
              event.target.value = "";
              if (!file) return;
              try {
                if (isExcelFile(file)) {
                  const draft = await readExcelImportDraft(file);
                  setExcelImportDraft(draft);
                  return;
                }
                setExcelImportDraft(null);
                const imported = await readRawDataFile(file, encoding);
                await onImportRawData(imported.content, imported.sourceName);
              } catch (error) {
                onImportError(error);
              }
            }}
          />
          <div className="analysis-raw-data-actions">
            <button className="secondary" type="button" disabled={importingRawData} onClick={() => uploadInputRef.current?.click()}>
              {importingRawData ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <FileText aria-hidden="true" />}
              上传数据文件
            </button>
            <button className="secondary" type="button" disabled={importingRawData || !pastedRawData.trim()} onClick={() => {
              setExcelImportDraft(null);
              onImportRawData(pastedRawData, "粘贴导入");
            }}>
              {importingRawData ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
              导入粘贴内容
            </button>
          </div>
          {excelImportDraft ? (
            <div className="analysis-excel-import">
              <div className="analysis-excel-import-header">
                <div>
                  <strong>{excelImportDraft.fileName}</strong>
                  <span>预览前 10 行，确认后写入 data/raw/随访数据.csv</span>
                </div>
                <button className="secondary" type="button" onClick={() => setExcelImportDraft(null)}>取消</button>
              </div>
              <label className="analysis-excel-sheet-picker">
                <span>工作表</span>
                <select value={excelImportDraft.selectedSheet} onChange={(event) => changeExcelSheet(event.target.value)}>
                  {excelImportDraft.sheetNames.map((sheetName) => (
                    <option key={sheetName} value={sheetName}>{sheetName}</option>
                  ))}
                </select>
              </label>
              <div className="analysis-excel-preview" aria-label="Excel 工作表预览">
                {excelPreviewRows.length ? (
                  <table>
                    <tbody>
                      {excelPreviewRows.map((row, rowIndex) => (
                        <tr key={`${excelImportDraft.selectedSheet}-${rowIndex}`}>
                          {Array.from({ length: excelPreviewColumnCount }).map((_, columnIndex) => (
                            <td key={columnIndex}>{row[columnIndex] || ""}</td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                ) : (
                  <span>当前工作表没有可预览的数据。</span>
                )}
              </div>
              <button type="button" disabled={importingRawData || !excelPreviewRows.length} onClick={confirmExcelImport}>
                {importingRawData ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
                确认导入当前工作表
              </button>
            </div>
          ) : null}
          <textarea
            className="analysis-raw-data-paste"
            value={pastedRawData}
            onChange={(event) => setPastedRawData(event.target.value)}
            placeholder="也可以直接粘贴 CSV 文本，例如：患者编号,治疗组,随访月数,结局..."
            aria-label="粘贴原始随访数据"
          />
        </article>

        <article className="analysis-cleaning-card">
          <div className="analysis-cleaning-card-title-inline">
            <h3>2. 自动识别建议</h3>
            <span>{detectedCount ? `已识别 ${detectedCount} 个字段` : "导入后自动生成"}</span>
          </div>
          {detectedSuggestions.length ? (
            <>
              <div className="analysis-detected-columns">
                {detectedColumns.map((column) => (
                  <span key={column.name} className="analysis-detected-column-chip">{column.name}</span>
                ))}
              </div>
              <div className="analysis-detected-list">
                {detectedSuggestions.map((item) => (
                  <div key={item.target} className="analysis-detected-item">
                    <div className="analysis-detected-item-header">
                      <strong><code>{item.target}</code></strong>
                      <span className={`analysis-status ${item.confidence === "高" ? "synced" : "neutral"}`}>置信度 {item.confidence}</span>
                    </div>
                    <div className="analysis-detected-item-source">{item.source}</div>
                    <div className="analysis-detected-item-meta">{item.reasons.join("，")}</div>
                  </div>
                ))}
              </div>
              <button className="secondary" type="button" onClick={onApplyDetectedMapping}>
                <Check aria-hidden="true" />
                套用识别结果
              </button>
            </>
          ) : (
            <ul>
              <li>导入 CSV 或 Excel 后，会根据表头和前几行样本推荐字段映射。</li>
              <li>这里只给推荐，不直接决定事件/删失等业务含义。</li>
              <li>业务规则仍由 Skill 指导智能体结合样本值生成。</li>
            </ul>
          )}
        </article>

        <article className="analysis-cleaning-card">
          <div className="analysis-cleaning-card-title-inline">
            <h3>3. 样本值映射建议</h3>
            <span>{valueSuggestions.length ? "写入映射配置时会一并保存" : "套用字段后自动生成"}</span>
          </div>
          {valueSuggestions.length ? (
            <div className="analysis-value-suggestion-list">
              {valueSuggestions.map((item) => (
                <div key={item.target} className="analysis-value-suggestion-item">
                  <div className="analysis-value-suggestion-header">
                    <strong><code>{item.target}</code></strong>
                    <span>{item.source}</span>
                  </div>
                  <div className="analysis-value-suggestion-tags">
                    {item.suggestions.map((suggestion) => (
                      <span
                        key={`${item.target}-${suggestion.raw}`}
                        className={`analysis-value-suggestion-tag ${suggestion.suggested ? "mapped" : "pending"}`}
                      >
                        {suggestion.raw} → {suggestion.suggested || "待确认"}
                      </span>
                    ))}
                  </div>
                  <div className="analysis-value-suggestion-meta">
                    {item.suggestions.map((suggestion) => `${suggestion.raw}：${suggestion.reason}`).join("；")}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <ul>
              <li>当前只对分组、事件、分期、性别生成值映射建议。</li>
              <li>无法确定业务含义的值会进入待确认，不会被静默归类。</li>
            </ul>
          )}
        </article>

        <article className="analysis-cleaning-card">
          <h3>4. 质量检查</h3>
          <ul>
            <li>基础检查保留：编号唯一、随访时间非负、事件 0/1、关键字段非空。</li>
            <li>业务规则由 Skill 指导智能体结合样本值生成。</li>
            <li>事件/删失/失访等不确定含义进入待确认列表。</li>
          </ul>
        </article>
      </section>

      <section className="analysis-cleaning-card analysis-cleaning-table-card">
        <div className="analysis-cleaning-card-title">
          <h3>5. 中文字段 → 标准变量</h3>
          <span>生存分析最小字段集</span>
        </div>
        <table className="analysis-cleaning-table">
          <thead>
            <tr><th>原始中文列</th><th>标准变量</th><th>类型</th><th>智能体提示</th></tr>
          </thead>
          <tbody>
            {fields.map((field) => (
              <tr key={field.target}>
                <td>
                  <input
                    type="text"
                    value={field.source}
                    onChange={(event) => onFieldSourceChange(field.target, event.target.value)}
                    aria-label={`${field.target} 的原始中文列名`}
                  />
                </td>
                <td><code>{field.target}</code></td>
                <td>{field.type}</td>
                <td>{field.hint}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="analysis-cleaning-code-card">
        <div className="analysis-cleaning-card-title">
          <h3>6. 自动生成 R 清洗代码</h3>
          <span>可写入项目并在 JupyterLab 运行</span>
        </div>
        <pre><code>{generatedCode}</code></pre>
      </section>
    </div>
  );
}

export const plugin = {
  id: "com.tma.r-survival-workbench",
  activate(context) {
    const client = backendClientFor(context);
    context.commands.register("com.tma.r-survival-workbench.create-project", async (input) => client.create({ ...input, notebookCode: DEFAULT_NOTEBOOK_CODE }));
  }
};

export function RSurvivalWorkbenchPage({ context }) {
  const repository = useMemo(() => repositoryFor(context.scope), [context]);
  const backendClient = useMemo(() => backendClientFor(context), [context]);
  const [projects, setProjects] = useState(() => {
    repository.ensureExample();
    return localProjects(repository);
  });
  const [projectID, setProjectID] = useState(() => projects[0]?.id || "");
  const project = projects.find((item) => item.id === projectID) || projects[0] || null;
  const rawDataContent = projectFile(project, "data/raw/随访数据.csv")?.content || "";
  const latestCleaningReport = projectFile(project, "reports/data-cleaning-summary.md")?.content || "";
  const [selectedFile, setSelectedFile] = useState(() => project?.activeFile || "");
  const [code, setCode] = useState(() => project?.notebookCode || "");
  const [centerView, setCenterView] = useState("notebook");
  const [cleaningFields, setCleaningFields] = useState(() => DEFAULT_CLEANING_FIELD_MAPPING.map((field) => ({ ...field })));
  const [cleaningEncoding, setCleaningEncoding] = useState("UTF-8");
  const detectedFieldProfile = useMemo(
    () => detectFieldSuggestions(rawDataContent),
    [rawDataContent]
  );
  const detectedValueSuggestions = useMemo(
    () => detectValueSuggestions(rawDataContent, cleaningFields),
    [cleaningFields, rawDataContent]
  );
  const cleaningCode = useMemo(
    () => buildDataCleaningRCode(cleaningFields, { encoding: cleaningEncoding, valueSuggestions: detectedValueSuggestions }),
    [cleaningEncoding, cleaningFields, detectedValueSuggestions]
  );
  const mappingYAML = useMemo(
    () => buildVariableMappingYAML(cleaningFields, { encoding: cleaningEncoding, valueSuggestions: detectedValueSuggestions }),
    [cleaningEncoding, cleaningFields, detectedValueSuggestions]
  );
  const cleaningReportSummary = useMemo(
    () => summarizeCleaningReport(latestCleaningReport),
    [latestCleaningReport]
  );
  const [sessions, setSessions] = useState([]);
  const [sessionID, setSessionID] = useState("");
  const [sessionLoading, setSessionLoading] = useState(true);
  const [prompt, setPrompt] = useState("检查当前生存分析代码，并说明需要补充的统计检验");
  const [messages, setMessages] = useState([
    { id: "welcome", role: "assistant", text: "选择一个 TMA Session 后，可结合当前项目和 R 代码继续分析。" }
  ]);
  const [sending, setSending] = useState(false);
  const [projectAction, setProjectAction] = useState("");
  const [gitLabConfigured, setGitLabConfigured] = useState(false);
  const sendAbortRef = useRef(null);
  const chatMessagesRef = useRef(null);
  const [highlightedMessageID, setHighlightedMessageID] = useState("");
  const [expandedDiffKeys, setExpandedDiffKeys] = useState([]);
  const fileTreeRef = useRef(null);
  const [spotlightFilePath, setSpotlightFilePath] = useState("");
  const [undoHistory, setUndoHistory] = useState([]);

  function toggleDiffPreview(key) {
    setExpandedDiffKeys((current) => (
      current.includes(key)
        ? current.filter((item) => item !== key)
        : [...current, key]
    ));
  }

  function buildAssistantContextPrompt(text) {
    return [
      "[R 语言生存分析工作台上下文]",
      `项目：${project?.name || ""}`,
      `目标：${project?.objective || "未填写"}`,
      `当前文件：${selectedFile || project?.activeFile || ""}`,
      `[项目 Skill] ${R_SURVIVAL_DATA_CLEANING_SKILL_PATH}`,
      survivalCleaningSkillContent(project),
      "",
      "[最近一次数据清洗报告]",
      formatCleaningReportContext(cleaningReportSummary),
      "",
      "当前 R 代码：",
      code,
      "",
      "如果你建议改项目文件，请使用可写回格式：",
      "### config/variable-mapping.yml",
      "```yaml",
      "# 完整文件内容",
      "```",
      "### R/clean-data.R",
      "```r",
      "# 完整文件内容",
      "```",
      "",
      `[用户请求] ${text}`
    ].join("\n");
  }

  async function submitAssistantPrompt(text, options = {}) {
    const messageText = String(text || "").trim();
    if (!messageText) return false;
    const {
      fallbackTitle = "已填入助手问题",
      fallbackMessage = "右侧输入框已更新，可直接发送。",
      successTitle = "问题已发送给助手",
      expectSuggestedFiles = false
    } = options;
    if (!project || !sessionID || sending) {
      setPrompt(messageText);
      context.notifications.show({
        level: "info",
        title: fallbackTitle,
        message: !project
          ? "当前还没有可用项目，已先放入输入框。"
          : !sessionID
            ? "请先选择关联任务，问题已先放入输入框。"
            : fallbackMessage
      });
      return false;
    }
    const userMessage = { id: `user-${Date.now()}`, role: "user", text: messageText };
    const pendingID = `assistant-${Date.now()}`;
    setMessages((current) => [...current, userMessage, { id: pendingID, role: "assistant", text: "正在分析…", pending: true }]);
    setPrompt("");
    setSending(true);
    sendAbortRef.current?.abort();
    const controller = new AbortController();
    sendAbortRef.current = controller;
    const contextualPrompt = buildAssistantContextPrompt(messageText);
    try {
      const started = await context.http.request(`/v2/sessions/${encodeURIComponent(sessionID)}/runs`, {
        method: "POST",
        signal: controller.signal,
        body: { input: { content: [{ type: "text", text: contextualPrompt }], attachments: [] } }
      });
      context.notifications.show({ level: "success", title: successTitle, message: "右侧助手已开始分析。" });
      const reply = await waitForAgentReply(context, sessionID, started.run.id, controller.signal);
      if (expectSuggestedFiles && extractSuggestedProjectFiles(reply).length) {
        setHighlightedMessageID(pendingID);
      }
      setMessages((current) => current.map((message) => message.id === pendingID ? { ...message, text: reply, pending: false } : message));
      return true;
    } catch (error) {
      if (error?.name === "AbortError") return false;
      setMessages((current) => current.map((message) => message.id === pendingID ? { ...message, text: error.message || String(error), pending: false, error: true } : message));
      return false;
    } finally {
      if (sendAbortRef.current === controller) sendAbortRef.current = null;
      setSending(false);
    }
  }

  async function refreshProjects(preferredID = projectID) {
    const response = await backendClient.list();
    setGitLabConfigured(response.gitLabConfigured);
    const next = [...response.projects, ...localProjects(repository)];
    setProjects(next);
    const preferred = next.find((item) => item.id === preferredID);
    if (preferred?.persistence === "backend") setProjectID(preferredID);
    else setProjectID(next.find((item) => item.persistence === "backend")?.id || preferred?.id || next[0]?.id || "");
    return next;
  }

  function replaceProject(updated) {
    setProjects((current) => current.map((item) => item.id === updated.id ? updated : item));
  }

  function commitUndoSnapshot(snapshot) {
    if (!snapshot) return;
    setUndoHistory((current) => [snapshot, ...current].slice(0, 20));
  }

  async function undoProjectMutation(index = 0) {
    const snapshot = undoHistory[index];
    if (!snapshot || !project || snapshot.projectID !== project.id) {
      context.notifications.show({ level: "info", title: "没有可撤销内容", message: "当前项目还没有可回退的自动写入操作。" });
      return;
    }
    setProjectAction("undo");
    try {
      const patch = {
        activeFile: snapshot.activeFile,
        notebookCode: snapshot.notebookCode,
        files: snapshot.files
      };
      const restored = project.persistence === "backend"
        ? await backendClient.update(project.id, patch)
        : { ...repository.update(project.id, patch), persistence: "local" };
      replaceProject(restored);
      setUndoHistory((current) => current.slice(index + 1));
      setSelectedFile(snapshot.activeFile);
      setCode(displayCodeForProject(restored));
      setSpotlightFilePath(snapshot.activeFile);
      if (snapshot.activeFile === "R/clean-data.R" || snapshot.activeFile === "config/variable-mapping.yml") setCenterView("cleaning");
      else setCenterView("notebook");
      context.notifications.show({ level: "success", title: "已撤销上一步", message: snapshot.label });
    } catch (error) {
      context.notifications.show({ level: "error", title: "撤销失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function undoLastProjectMutation() {
    await undoProjectMutation(0);
  }

  useEffect(() => {
    let active = true;
    backendClient.list().then((response) => {
      if (!active) return;
      setGitLabConfigured(response.gitLabConfigured);
      const next = [...response.projects, ...localProjects(repository)];
      setProjects(next);
      setProjectID((current) => {
        const selected = next.find((item) => item.id === current);
        if (selected?.persistence === "backend") return current;
        return next.find((item) => item.persistence === "backend")?.id || selected?.id || next[0]?.id || "";
      });
    }).catch((error) => {
      if (active) context.notifications.show({ level: "error", title: "项目加载失败", message: error.message || String(error) });
    });
    return () => { active = false; };
  }, [backendClient, context, repository]);

  useEffect(() => {
    if (!project) return;
    setSelectedFile(project.activeFile);
    setCode(displayCodeForProject(project));
  }, [project?.id, project?.updatedAt]);

  useEffect(() => {
    let active = true;
    setSessionLoading(true);
    context.tasks.list({ workspaceId: context.scope.workspaceId, includeArchived: false, limit: 40 }).then((items) => {
      if (!active) return;
      const next = Array.isArray(items) ? items : [];
      setSessions(next);
      setSessionID((current) => current || next[0]?.id || "");
    }).catch((error) => {
      if (active) context.notifications.show({ level: "error", title: "任务加载失败", message: error.message || String(error) });
    }).finally(() => {
      if (active) setSessionLoading(false);
    });
    return () => {
      active = false;
      sendAbortRef.current?.abort();
    };
  }, [context]);

  useEffect(() => {
    const container = chatMessagesRef.current;
    if (!container) return;
    container.scrollTo({ top: container.scrollHeight, behavior: "smooth" });
  }, [messages]);

  useEffect(() => {
    if (!spotlightFilePath) return undefined;
    const timer = window.setTimeout(() => setSpotlightFilePath(""), 2600);
    return () => window.clearTimeout(timer);
  }, [spotlightFilePath]);

  useEffect(() => {
    if (!spotlightFilePath) return;
    const container = fileTreeRef.current;
    const target = container?.querySelector(`[data-file-path="${CSS.escape(spotlightFilePath)}"]`);
    if (target) target.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [spotlightFilePath, project?.id]);

  async function createProject() {
    const values = await context.dialog.form(projectForm());
    if (!values) return;
    setProjectAction("create");
    try {
      const created = await context.commands.execute("com.tma.r-survival-workbench.create-project", values);
      await refreshProjects(created.id);
      setSelectedFile(created.activeFile);
      setCode(created.notebookCode);
      context.notifications.show({
        level: created.gitStatus === "error" ? "error" : "success",
        title: created.gitStatus === "synced" ? "项目与 GitLab 已创建" : "分析项目已创建",
        message: created.gitError || created.name
      });
    } catch (error) {
      context.notifications.show({ level: "error", title: "项目创建失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function configureProject() {
    if (!project) return;
    const values = await context.dialog.form(settingsForm(project));
    if (!values) return;
    setProjectAction("settings");
    try {
      const updated = project.persistence === "backend"
        ? await backendClient.update(project.id, values)
        : { ...repository.update(project.id, values), persistence: "local" };
      replaceProject(updated);
      context.notifications.show({ level: "success", title: "项目连接已保存", message: updated.name });
    } catch (error) {
      context.notifications.show({ level: "error", title: "项目设置保存失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  function selectFile(file) {
    if (file.kind === "folder" || !project) return;
    setSelectedFile(file.path);
    setCode(file.content || (file.path.endsWith(".ipynb") ? project.notebookCode : ""));
    if (file.path === "R/clean-data.R" || file.path === "config/variable-mapping.yml") setCenterView("cleaning");
    else if (file.path.endsWith(".ipynb") || file.path.endsWith(".R")) setCenterView("notebook");
    if (project.persistence === "backend") {
      backendClient.update(project.id, { activeFile: file.path }).then(replaceProject).catch((error) => {
        context.notifications.show({ level: "error", title: "文件状态保存失败", message: error.message || String(error) });
      });
    } else {
      replaceProject({ ...repository.update(project.id, { activeFile: file.path }), persistence: "local" });
    }
  }

  function updateCleaningFieldSource(target, source) {
    setCleaningFields((current) => current.map((field) => field.target === target ? { ...field, source } : field));
  }

  function applyDetectedFieldMapping() {
    if (!detectedFieldProfile.suggestions.length) {
      context.notifications.show({ level: "info", title: "暂无可套用的识别结果", message: "请先导入包含表头和样本数据的 CSV 或 Excel。" });
      return;
    }
    const suggestionMap = new Map(detectedFieldProfile.suggestions.map((item) => [item.target, item.source]));
    setCleaningFields((current) => current.map((field) => (
      suggestionMap.has(field.target)
        ? { ...field, source: suggestionMap.get(field.target) }
        : field
    )));
    context.notifications.show({
      level: "success",
      title: "识别结果已套用",
      message: detectedFieldProfile.suggestions.map((item) => `${item.target} ← ${item.source}`).join("，")
    });
  }

  async function applyCleaningTemplate(generatedCode = cleaningCode) {
    if (!project) return;
    const undoSnapshot = snapshotProjectForUndo(project, "撤销写入 R/clean-data.R");
    const nextCode = generatedCode;
    const nextFiles = withProjectFileContent(project.files, "R/clean-data.R", nextCode);
    setProjectAction("save");
    setSelectedFile("R/clean-data.R");
    setCode(nextCode);
    try {
      const updated = project.persistence === "backend"
        ? await backendClient.update(project.id, { activeFile: "R/clean-data.R", files: nextFiles })
        : { ...repository.update(project.id, { activeFile: "R/clean-data.R", files: nextFiles }), persistence: "local" };
      replaceProject(updated);
      commitUndoSnapshot(undoSnapshot);
      context.notifications.show({ level: "success", title: "清洗模板已写入", message: "已保存到当前项目，可切到 JupyterLab 运行。" });
    } catch (error) {
      context.notifications.show({ level: "error", title: "清洗模板保存失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function applyMappingTemplate(generatedYAML = mappingYAML) {
    if (!project) return;
    const undoSnapshot = snapshotProjectForUndo(project, "撤销写入 config/variable-mapping.yml");
    const nextFiles = withProjectFileContent(project.files, "config/variable-mapping.yml", generatedYAML);
    setProjectAction("save-mapping");
    setSelectedFile("config/variable-mapping.yml");
    setCode(generatedYAML);
    try {
      const updated = project.persistence === "backend"
        ? await backendClient.update(project.id, { activeFile: "config/variable-mapping.yml", files: nextFiles })
        : { ...repository.update(project.id, { activeFile: "config/variable-mapping.yml", files: nextFiles }), persistence: "local" };
      replaceProject(updated);
      commitUndoSnapshot(undoSnapshot);
      context.notifications.show({ level: "success", title: "映射配置已写入", message: "已保存到 config/variable-mapping.yml。" });
    } catch (error) {
      context.notifications.show({ level: "error", title: "映射配置保存失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function applySkillTemplate(skillContent = R_SURVIVAL_DATA_CLEANING_SKILL_CONTENT) {
    if (!project) return;
    const undoSnapshot = snapshotProjectForUndo(project, `撤销写入 ${R_SURVIVAL_DATA_CLEANING_SKILL_PATH}`);
    const nextFiles = withProjectFileContent(project.files, R_SURVIVAL_DATA_CLEANING_SKILL_PATH, skillContent);
    setProjectAction("save-skill");
    setSelectedFile(R_SURVIVAL_DATA_CLEANING_SKILL_PATH);
    setCode(skillContent);
    try {
      const updated = project.persistence === "backend"
        ? await backendClient.update(project.id, { activeFile: R_SURVIVAL_DATA_CLEANING_SKILL_PATH, files: nextFiles })
        : { ...repository.update(project.id, { activeFile: R_SURVIVAL_DATA_CLEANING_SKILL_PATH, files: nextFiles }), persistence: "local" };
      replaceProject(updated);
      commitUndoSnapshot(undoSnapshot);
      context.notifications.show({ level: "success", title: "Skill 已写入项目", message: R_SURVIVAL_DATA_CLEANING_SKILL_PATH });
    } catch (error) {
      context.notifications.show({ level: "error", title: "Skill 保存失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function writeSampleRawData(sampleCSV = DEFAULT_RAW_FOLLOWUP_CSV) {
    if (!project) return;
    const undoSnapshot = snapshotProjectForUndo(project, "撤销写入示例原始数据");
    const nextFiles = withProjectFileContent(project.files, "data/raw/随访数据.csv", sampleCSV);
    setProjectAction("save-sample-data");
    setSelectedFile("data/raw/随访数据.csv");
    setCode(sampleCSV);
    try {
      const updated = project.persistence === "backend"
        ? await backendClient.update(project.id, { activeFile: "data/raw/随访数据.csv", files: nextFiles })
        : { ...repository.update(project.id, { activeFile: "data/raw/随访数据.csv", files: nextFiles }), persistence: "local" };
      replaceProject(updated);
      commitUndoSnapshot(undoSnapshot);
      setCenterView("notebook");
      context.notifications.show({ level: "success", title: "示例数据已写入", message: "已保存到 data/raw/随访数据.csv。" });
    } catch (error) {
      context.notifications.show({ level: "error", title: "示例数据保存失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function importRawData(content, sourceName = "原始数据") {
    if (!project) return;
    const normalized = String(content || "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    if (!normalized.trim()) {
      context.notifications.show({ level: "info", title: "原始数据为空", message: "请选择非空 CSV/TXT 文件，或粘贴表格文本。" });
      return;
    }
    const undoSnapshot = snapshotProjectForUndo(project, `撤销导入原始数据（${sourceName}）`);
    const nextFiles = withProjectFileContent(project.files, "data/raw/随访数据.csv", normalized);
    setProjectAction("import-raw-data");
    setSelectedFile("data/raw/随访数据.csv");
    setCode(normalized);
    try {
      const updated = project.persistence === "backend"
        ? await backendClient.update(project.id, { activeFile: "data/raw/随访数据.csv", files: nextFiles })
        : { ...repository.update(project.id, { activeFile: "data/raw/随访数据.csv", files: nextFiles }), persistence: "local" };
      replaceProject(updated);
      commitUndoSnapshot(undoSnapshot);
      setCenterView("notebook");
      context.notifications.show({ level: "success", title: "原始数据已导入", message: `${sourceName} 已保存到 data/raw/随访数据.csv。` });
    } catch (error) {
      context.notifications.show({ level: "error", title: "原始数据导入失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function applySuggestedFiles(suggestedFiles) {
    if (!project) return;
    if (!suggestedFiles.length) {
      context.notifications.show({ level: "info", title: "没有可写回的文件", message: "助手回复中未找到明确的项目文件代码块。" });
      return;
    }
    const undoSnapshot = snapshotProjectForUndo(project, `撤销助手写回（${suggestedFiles.map((file) => basename(file.path)).join("、")}）`);
    const first = suggestedFiles[0];
    const nextFiles = suggestedFiles.reduce(
      (files, file) => withProjectFileContent(files, file.path, file.content),
      project.files
    );
    setProjectAction("apply-ai-files");
    setSelectedFile(first.path);
    setCode(first.content);
    setSpotlightFilePath(first.path);
    if (first.path === "R/clean-data.R" || first.path === "config/variable-mapping.yml") setCenterView("cleaning");
    else setCenterView("notebook");
    try {
      const updated = project.persistence === "backend"
        ? await backendClient.update(project.id, { activeFile: first.path, files: nextFiles })
        : { ...repository.update(project.id, { activeFile: first.path, files: nextFiles }), persistence: "local" };
      replaceProject(updated);
      commitUndoSnapshot(undoSnapshot);
      setHighlightedMessageID("");
      context.notifications.show({
        level: "success",
        title: "助手建议已写回",
        message: suggestedFiles.map((file) => file.path).join("、")
      });
    } catch (error) {
      context.notifications.show({ level: "error", title: "助手建议写回失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function applySuggestedFilesFromReply(replyText) {
    const suggestedFiles = extractSuggestedProjectFiles(replyText);
    await applySuggestedFiles(suggestedFiles);
  }

  async function saveCode() {
    if (!project) return;
    const selected = selectedFile || project.activeFile;
    const selectedProjectFile = projectFile(project, selected);
    const nextPatch = selectedProjectFile && !selected.endsWith(".ipynb")
      ? { files: withProjectFileContent(project.files, selected, code), activeFile: selected }
      : { notebookCode: code, activeFile: selected };
    if (!nextPatch.files && code === project.notebookCode) return;
    if (nextPatch.files && selectedProjectFile?.content === code && selected === project.activeFile) return;
    setProjectAction("save");
    try {
      const updated = project.persistence === "backend"
        ? await backendClient.update(project.id, nextPatch)
        : { ...repository.update(project.id, nextPatch), persistence: "local" };
      replaceProject(updated);
      context.notifications.show({ level: "success", title: selected.endsWith(".ipynb") ? "Notebook 已保存" : "文件已保存", message: selected });
    } catch (error) {
      context.notifications.show({ level: "error", title: "Notebook 保存失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function syncProject() {
    if (!project || project.persistence !== "backend") {
      context.notifications.show({ level: "info", title: "本地示例", message: "新建后端项目后可连接 GitLab。" });
      return;
    }
    if (!gitLabConfigured) {
      context.notifications.show({ level: "info", title: "GitLab 待配置", message: "请在服务端配置 TMA_GITLAB_TOKEN 后重试。" });
      return;
    }
    setProjectAction("sync");
    try {
      const updated = await backendClient.sync(project.id);
      replaceProject(updated);
      context.notifications.show({
        level: updated.gitStatus === "synced" ? "success" : "error",
        title: updated.gitStatus === "synced" ? "GitLab 同步完成" : "GitLab 同步失败",
        message: updated.gitError || updated.gitlabURL || updated.name
      });
    } finally {
      setProjectAction("");
    }
  }

  async function startRuntime() {
    if (!project || project.persistence !== "backend") return;
    setProjectAction("runtime-start");
    try {
      const updated = await backendClient.startRuntime(project.id);
      replaceProject(updated);
      if (updated.runtimeStatus === "error") {
        context.notifications.show({ level: "error", title: "Runtime 启动失败", message: updated.runtimeError || "R Runtime 启动失败" });
        return;
      }
      setCenterView("runtime");
      context.notifications.show({ level: "success", title: "Runtime 已启动", message: updated.runtimeURL || updated.notebookURL || updated.name });
    } catch (error) {
      context.notifications.show({ level: "error", title: "Runtime 启动失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function stopRuntime() {
    if (!project || project.persistence !== "backend") return;
    setProjectAction("runtime-stop");
    try {
      const updated = await backendClient.stopRuntime(project.id);
      replaceProject(updated);
      context.notifications.show({ level: "success", title: "Runtime 已停止", message: updated.name });
    } catch (error) {
      context.notifications.show({ level: "error", title: "Runtime 停止失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function runCleaningWorkflow() {
    if (!project) return;
    if (project.persistence !== "backend") {
      context.notifications.show({ level: "info", title: "需要后端项目", message: "请先新建项目；本地示例不能启动 R Runtime。" });
      return;
    }
    setProjectAction("run-cleaning");
    try {
      let files = withProjectFileContent(project.files, "R/clean-data.R", cleaningCode);
      files = withProjectFileContent(files, "config/variable-mapping.yml", mappingYAML);
      if (!projectFile({ files }, R_SURVIVAL_DATA_CLEANING_SKILL_PATH)) {
        files = withProjectFileContent(files, R_SURVIVAL_DATA_CLEANING_SKILL_PATH, R_SURVIVAL_DATA_CLEANING_SKILL_CONTENT);
      }
      let updated = await backendClient.update(project.id, { activeFile: "R/clean-data.R", files });
      replaceProject(updated);
      if (updated.runtimeStatus !== "running") {
        updated = await backendClient.startRuntime(updated.id);
        replaceProject(updated);
        if (updated.runtimeStatus === "error") {
          context.notifications.show({ level: "error", title: "Runtime 启动失败", message: updated.runtimeError || "R Runtime 启动失败" });
          return;
        }
      }
      const response = await backendClient.runCleaning(updated.id);
      replaceProject(response.project);
      const reportFile = projectFile(response.project, "reports/data-cleaning-summary.md");
      setSelectedFile("reports/data-cleaning-summary.md");
      setCode(reportFile?.content || response.result?.stdout || "");
      setCenterView("notebook");
      setMessages((current) => [...current, {
        id: `assistant-cleaning-${Date.now()}`,
        role: "assistant",
        text: response.result?.exit_code === 0
          ? "清洗流程执行完成，报告已写入 reports/data-cleaning-summary.md。"
          : `清洗流程执行失败，退出码 ${response.result?.exit_code ?? "未知"}；错误日志已写入 reports/data-cleaning-summary.md。`
      }]);
      context.notifications.show({
        level: response.result?.exit_code === 0 ? "success" : "error",
        title: response.result?.exit_code === 0 ? "清洗流程完成" : "清洗流程失败",
        message: "报告已保存到 reports/data-cleaning-summary.md"
      });
    } catch (error) {
      context.notifications.show({ level: "error", title: "清洗流程运行失败", message: error.message || String(error) });
    } finally {
      setProjectAction("");
    }
  }

  async function refreshSessions() {
    setSessionLoading(true);
    try {
      const next = await context.tasks.list({ workspaceId: context.scope.workspaceId, includeArchived: false, limit: 40 });
      setSessions(Array.isArray(next) ? next : []);
      if (!sessionID && next?.[0]?.id) setSessionID(next[0].id);
    } finally {
      setSessionLoading(false);
    }
  }

  async function sendMessage(event) {
    event.preventDefault();
    await submitAssistantPrompt(prompt, {
      fallbackTitle: "请先补充问题",
      fallbackMessage: "右侧输入框已更新，可直接发送。",
      successTitle: "消息已发送给助手"
    });
  }

  if (!project) return <div className="analysis-workbench-empty">没有可用项目。</div>;
  const runtimeAvailable = project.persistence === "backend";
  const activeProjectFile = projectFile(project, selectedFile || project.activeFile);
  const rawDataFile = projectFile(project, "data/raw/随访数据.csv");
  const skillInstalled = Boolean(projectFile(project, R_SURVIVAL_DATA_CLEANING_SKILL_PATH));
  const projectUndoHistory = undoHistory.filter((item) => item.projectID === project.id).slice(0, 6);
  const showFileEditor = Boolean(activeProjectFile && !(selectedFile || project.activeFile).endsWith(".ipynb") && centerView === "notebook");
  const fileDirty = activeProjectFile
    ? code !== (activeProjectFile.content || "")
    : code !== (project.notebookCode || "");

  return (
    <div className="analysis-workbench-page">
      <header className="analysis-workbench-toolbar">
        <div className="analysis-workbench-project-picker">
          <label htmlFor="analysis-project-select">项目</label>
          <div className="analysis-select-wrap">
            <select id="analysis-project-select" value={project.id} onChange={(event) => setProjectID(event.target.value)}>
              {projects.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}
            </select>
            <ChevronDown aria-hidden="true" />
          </div>
          <span className={`analysis-status ${project.gitStatus}`} title={project.gitError || ""}>{statusLabel(project)}</span>
        </div>
        <div className="analysis-workbench-actions">
          <button
            className="secondary"
            type="button"
            disabled={Boolean(projectAction) || !undoHistory.length}
            onClick={undoLastProjectMutation}
            title={undoHistory[0]?.label || "没有可撤销内容"}
            aria-label="撤销上一步写入"
          >
            <RefreshCw aria-hidden="true" />
            撤销上一步
          </button>
          <button className="secondary" type="button" disabled={Boolean(projectAction)} onClick={configureProject}><Settings2 aria-hidden="true" />项目设置</button>
          {runtimeAvailable ? (
            project.runtimeStatus === "running" ? (
              <button className="secondary" type="button" disabled={Boolean(projectAction)} onClick={stopRuntime} title={project.runtimeError || "停止 R Runtime"} aria-label="停止 R Runtime">
                {projectAction === "runtime-stop" ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Square aria-hidden="true" />}
                停止 Runtime
              </button>
            ) : (
              <button className="secondary" type="button" disabled={Boolean(projectAction)} onClick={startRuntime} title={project.runtimeStatus === "error" ? project.runtimeError || "重试启动 R Runtime" : "启动 R Runtime"} aria-label="启动 R Runtime">
                {projectAction === "runtime-start" ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Play aria-hidden="true" />}
                启动 Runtime
              </button>
            )
          ) : (
            <button className="secondary" type="button" disabled title="本地示例不可启动 Runtime" aria-label="启动 R Runtime">
              <Play aria-hidden="true" />
              启动 Runtime
            </button>
          )}
          <button type="button" disabled={Boolean(projectAction)} onClick={createProject}>{projectAction === "create" ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Plus aria-hidden="true" />}新建项目</button>
        </div>
      </header>

      <div className="analysis-workbench-grid">
        <aside className="analysis-project-pane" aria-label="项目文件">
          <div className="analysis-pane-heading">
            <div><PanelLeft aria-hidden="true" /><strong>项目目录</strong></div>
            <button className="analysis-icon-button" type="button" disabled={Boolean(projectAction)} onClick={() => syncProject().catch((error) => context.notifications.show({ level: "error", title: "GitLab 同步失败", message: error.message || String(error) }))} aria-label="同步 GitLab 项目" title="同步 GitLab 项目">{projectAction === "sync" ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}</button>
          </div>

          <div className="analysis-repository-meta">
            <GitBranch aria-hidden="true" />
            <span>{project.branch}</span>
            <code>{project.repositoryPath}</code>
          </div>

          <div className="analysis-file-tree" role="tree" aria-label="项目目录" ref={fileTreeRef}>
            {project.files.map((file) => {
              const depth = Math.max(0, file.path.split("/").length - 1);
              const active = file.path === selectedFile;
              const spotlighted = file.path === spotlightFilePath;
              return (
                <button
                  className={`analysis-file-row ${active ? "active" : ""} ${spotlighted ? "spotlight" : ""}`}
                  style={{ "--analysis-file-depth": depth }}
                  type="button"
                  role="treeitem"
                  aria-selected={active}
                  onClick={() => selectFile(file)}
                  key={file.path}
                  data-file-path={file.path}
                >
                  {fileIcon(file.path, file.kind)}
                  <span>{basename(file.path)}</span>
                  {file.status === "modified" ? <span className="analysis-file-change">M</span> : null}
                </button>
              );
            })}
          </div>

          <div className="analysis-git-summary">
            <div><GitCommitHorizontal aria-hidden="true" /><strong>{project.gitStatus === "synced" ? "仓库状态" : "待提交变更"}</strong><span>{project.files.filter((file) => file.status === "modified").length}</span></div>
            <p>{project.gitStatus === "synced" ? "R 生存分析模板已提交到 GitLab。" : project.persistence === "backend" ? "项目已由后端保存，可在配置 GitLab 后重试同步。" : "本地示例仅用于预览，新建项目将由后端持久化。"}</p>
          </div>

          <div className="analysis-undo-history">
            <div className="analysis-undo-history-header">
              <strong>撤销历史</strong>
              <span>{projectUndoHistory.length ? `最近 ${projectUndoHistory.length} 次` : "暂无"}</span>
            </div>
            {projectUndoHistory.length ? (
              <div className="analysis-undo-history-list">
                {projectUndoHistory.map((entry, index) => (
                  <button
                    key={`${entry.createdAt}-${index}`}
                    type="button"
                    className="analysis-undo-history-item"
                    disabled={Boolean(projectAction)}
                    onClick={() => undoProjectMutation(index)}
                    title={entry.label}
                  >
                    <div>
                      <strong>{index === 0 ? "上一步" : `回退到第 ${index + 1} 步前`}</strong>
                      <span>{entry.label}</span>
                    </div>
                    <time>{formatUndoTimestamp(entry.createdAt)}</time>
                  </button>
                ))}
              </div>
            ) : (
              <p className="analysis-undo-history-empty">自动写入后，这里会保留最近几次可回退操作。</p>
            )}
          </div>
        </aside>

        <main className="analysis-notebook-pane">
          <div className="analysis-pane-heading analysis-notebook-heading">
            <div className="analysis-view-tabs" role="tablist" aria-label="分析视图">
              <button className={centerView === "notebook" ? "active" : ""} type="button" role="tab" aria-selected={centerView === "notebook"} onClick={() => setCenterView("notebook")}><FileCode2 aria-hidden="true" />Notebook</button>
              <button className={centerView === "cleaning" ? "active" : ""} type="button" role="tab" aria-selected={centerView === "cleaning"} onClick={() => setCenterView("cleaning")}><Sheet aria-hidden="true" />数据清洗</button>
              <button className={centerView === "runtime" ? "active" : ""} type="button" role="tab" aria-selected={centerView === "runtime"} onClick={() => setCenterView("runtime")}><TerminalSquare aria-hidden="true" />JupyterLab</button>
            </div>
            <div className={`analysis-runtime-state ${project.runtimeStatus || ""}`}>
              {project.runtimeStatus === "running" ? <Check aria-hidden="true" /> : project.runtimeStatus === "starting" ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : project.runtimeStatus === "error" ? <CircleAlert aria-hidden="true" /> : project.runtimeStatus === "stopped" ? <Square aria-hidden="true" /> : project.notebookURL ? <Check aria-hidden="true" /> : <CircleAlert aria-hidden="true" />}
              <span title={project.runtimeError || ""}>{runtimeLabel(project)}</span>
            </div>
          </div>
          {showFileEditor ? (
            <FileEditor
              code={code}
              dirty={fileDirty}
              highlighted={(selectedFile || project.activeFile) === spotlightFilePath}
              path={selectedFile || project.activeFile}
              saving={projectAction === "save"}
              onAskAI={() => {
                setPrompt(`请检查当前项目文件 ${selectedFile || project.activeFile} 的内容，指出语法、数据清洗和生存分析流程风险。\n\n文件内容：\n${code}`);
              }}
              onCodeChange={setCode}
              onCodeSave={saveCode}
              onOpenRuntime={() => setCenterView("runtime")}
              runtimeAvailable={Boolean(project.notebookURL)}
            />
          ) : centerView === "notebook" ? (
            <NotebookPreview
              code={code}
              onCodeChange={setCode}
              onCodeSave={saveCode}
              onOpenRuntime={() => setCenterView("runtime")}
              runtimeAvailable={Boolean(project.notebookURL)}
            />
          ) : centerView === "cleaning" ? (
            <DataCleaningWorkbench
              detectedColumns={detectedFieldProfile.columns}
              detectedSuggestions={detectedFieldProfile.suggestions}
              encoding={cleaningEncoding}
              fields={cleaningFields}
              generatedCode={cleaningCode}
              importingRawData={projectAction === "import-raw-data"}
              rawDataFile={rawDataFile}
              onApplyDetectedMapping={applyDetectedFieldMapping}
              onInspectCleaningImpact={() => submitAssistantPrompt([
                "请基于最近一次数据清洗报告，评估清洗对后续生存分析的影响。",
                "",
                "重点说明：",
                "1. 重复编号移除和无效记录移除是否会影响样本代表性；",
                "2. 是否需要补做缺失值或异常值处理；",
                "3. 下一步最值得优先处理的风险。"
              ].join("\n"), {
                fallbackTitle: "已填入清洗影响分析问题",
                successTitle: "已发送清洗影响分析问题"
              })}
              onInspectFailedChecks={() => submitAssistantPrompt([
                "请根据最近一次数据清洗报告中的失败质量检查，给出修复建议。",
                "",
                "请输出：",
                "1. 每个失败检查的可能原因；",
                "2. 推荐修改的数据映射或 R 清洗脚本；",
                "3. 如果需要改项目文件，请用可写回格式输出。"
              ].join("\n"), {
                fallbackTitle: "已填入质量检查修复问题",
                successTitle: "已发送质量检查修复问题"
              })}
              onInspectPendingValues={() => submitAssistantPrompt([
                "请根据最近一次数据清洗报告中的待确认取值，生成处理建议。",
                "",
                "请按字段逐项说明：",
                "1. 这些取值更像事件、删失还是未知；",
                "2. 建议写入 variable-mapping.yml 的 value_map / unresolved_values；",
                "3. 仍需用户确认的问题。"
              ].join("\n"), {
                fallbackTitle: "已填入待确认值处理问题",
                successTitle: "已发送待确认值处理问题"
              })}
              onDraftPendingValues={() => submitAssistantPrompt([
                "请根据最近一次数据清洗报告中的待确认取值，直接生成下一版映射配置草案。",
                "",
                "要求：",
                "1. 优先输出 `### config/variable-mapping.yml` 的完整内容；",
                "2. 对不确定取值保留到 unresolved_values；",
                "3. 如仍需用户确认，请先列出最少问题，再给草案。"
              ].join("\n"), {
                fallbackTitle: "已填入映射草案生成问题",
                successTitle: "已发送映射草案生成问题",
                expectSuggestedFiles: true
              })}
              onInspectSummary={() => submitAssistantPrompt([
                "请解读最近一次数据清洗报告。",
                "",
                "请用简洁中文总结：",
                "1. 当前数据是否已适合进入生存分析；",
                "2. 最主要的质量风险；",
                "3. 接下来建议先做哪一步。"
              ].join("\n"), {
                fallbackTitle: "已填入报告解读问题",
                successTitle: "已发送报告解读问题"
              })}
              onDraftFailedChecks={() => submitAssistantPrompt([
                "请根据最近一次数据清洗报告中的失败质量检查，直接生成清洗脚本修订稿。",
                "",
                "要求：",
                "1. 优先输出 `### R/clean-data.R` 的完整内容；",
                "2. 需要时也可同时输出 `### config/variable-mapping.yml`；",
                "3. 修订应优先解决失败检查，并保留待确认项。"
              ].join("\n"), {
                fallbackTitle: "已填入脚本修订生成问题",
                successTitle: "已发送脚本修订生成问题",
                expectSuggestedFiles: true
              })}
              saving={projectAction === "save"}
              savingMapping={projectAction === "save-mapping"}
              savingSampleData={projectAction === "save-sample-data"}
              savingSkill={projectAction === "save-skill"}
              skillInstalled={skillInstalled}
              valueSuggestions={detectedValueSuggestions}
              onApplyMapping={() => applyMappingTemplate(mappingYAML)}
              onApplySkill={() => applySkillTemplate()}
              onApplyTemplate={() => applyCleaningTemplate(cleaningCode)}
              onImportError={(error) => context.notifications.show({ level: "error", title: "原始数据读取失败", message: error.message || String(error) })}
              onImportRawData={importRawData}
              onWriteSampleData={() => writeSampleRawData()}
              onRunCleaning={runCleaningWorkflow}
              runningCleaning={projectAction === "run-cleaning"}
              onAskAI={() => {
                setPrompt(buildSurvivalCleaningAgentPrompt({
                  project,
                  fields: cleaningFields,
                  encoding: cleaningEncoding,
                  mappingYAML,
                  reportSummary: cleaningReportSummary,
                  valueSuggestions: detectedValueSuggestions,
                  code: cleaningCode,
                  request: "请按 Skill 为当前中文随访数据生成下一版数据清洗方案，不确定的医学/业务规则列为待确认项。"
                }));
              }}
              onEncodingChange={setCleaningEncoding}
              onFieldSourceChange={updateCleaningFieldSource}
            />
          ) : <RuntimeFrame project={project} />}
        </main>

        <aside className="analysis-chat-pane" aria-label="AI 分析助手">
          <div className="analysis-pane-heading">
            <div><MessageSquare aria-hidden="true" /><strong>AI 分析助手</strong></div>
            <span className="analysis-status agent"><CircleDot aria-hidden="true" />TMA Agent</span>
          </div>

          <div className="analysis-session-picker">
            <label htmlFor="analysis-session-select">关联任务</label>
            <div>
              <select id="analysis-session-select" value={sessionID} disabled={sessionLoading} onChange={(event) => setSessionID(event.target.value)}>
                <option value="">{sessionLoading ? "正在加载…" : "选择 TMA Session"}</option>
                {sessions.map((session) => <option value={session.id} key={session.id}>{session.title || session.id}</option>)}
              </select>
              <button
                className="analysis-icon-button"
                type="button"
                disabled={sessionLoading}
                onClick={() => refreshSessions().catch((error) => context.notifications.show({
                  level: "error",
                  title: "刷新失败",
                  message: error.message || String(error)
                }))}
                aria-label="刷新任务"
                title="刷新任务"
              >
                <RefreshCw aria-hidden="true" />
              </button>
            </div>
          </div>

          <div className="analysis-chat-messages" aria-live="polite" ref={chatMessagesRef}>
            {messages.map((message) => {
              const suggestedFiles = message.role === "assistant" && !message.pending && !message.error
                ? extractSuggestedProjectFiles(message.text)
                : [];
              return (
                <article className={`analysis-chat-message ${message.role} ${message.pending ? "pending" : ""} ${message.error ? "error" : ""} ${message.id === highlightedMessageID ? "highlighted" : ""}`} key={message.id}>
                  <div>{message.role === "assistant" ? <Bot aria-hidden="true" /> : <Sparkles aria-hidden="true" />}<strong>{message.role === "assistant" ? "分析助手" : "你"}</strong></div>
                  <p>{message.text}</p>
                  {suggestedFiles.length ? (
                    <div className={`analysis-chat-message-actions ${message.id === highlightedMessageID ? "highlighted" : ""}`}>
                      <span>{suggestedFiles.map((file) => file.path).join("、")}</span>
                      <button type="button" disabled={Boolean(projectAction)} onClick={() => applySuggestedFilesFromReply(message.text)}>
                        {projectAction === "apply-ai-files" ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : <Check aria-hidden="true" />}
                        全部写回
                      </button>
                      <div className="analysis-chat-message-file-actions">
                        {suggestedFiles.map((file) => {
                          const currentContent = projectFile(project, file.path)?.content || "";
                          const diff = buildSuggestedFileDiff(currentContent, file.content);
                          const diffKey = `${message.id}:${file.path}`;
                          const expanded = expandedDiffKeys.includes(diffKey);
                          return (
                            <div className="analysis-chat-message-file-card" key={file.path}>
                              <button
                                type="button"
                                className="secondary"
                                disabled={Boolean(projectAction)}
                                onClick={() => applySuggestedFiles([file])}
                              >
                                <FileText aria-hidden="true" />
                                只写回 {basename(file.path)}
                              </button>
                              {diff.changed ? (
                                <div className="analysis-chat-message-diff">
                                  <div className="analysis-chat-message-diff-header">
                                    <span>第 {diff.startLine} 行附近有变化</span>
                                    <button type="button" className="secondary" onClick={() => toggleDiffPreview(diffKey)}>
                                      {expanded ? "收起完整内容" : "展开完整内容"}
                                    </button>
                                  </div>
                                  <div className="analysis-chat-message-diff-grid">
                                    <div>
                                      <strong>当前</strong>
                                      <pre>{(expanded ? currentContent : diff.currentChanged.slice(0, 8).join("\n")) || "(空)"}</pre>
                                    </div>
                                    <div>
                                      <strong>建议</strong>
                                      <pre>{(expanded ? file.content : diff.nextChanged.slice(0, 8).join("\n")) || "(空)"}</pre>
                                    </div>
                                  </div>
                                </div>
                              ) : (
                                <div className="analysis-chat-message-diff unchanged">
                                  <span>与当前文件一致</span>
                                </div>
                              )}
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  ) : null}
                  {message.pending ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : null}
                </article>
              );
            })}
          </div>

          <form className="analysis-chat-composer" onSubmit={sendMessage}>
            <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder="结合当前 Notebook 继续分析…" aria-label="发送给分析助手" />
            <div>
              <span><Cloud aria-hidden="true" />包含当前项目上下文</span>
              <button type="submit" disabled={!sessionID || !prompt.trim() || sending} aria-label="发送消息"><Send aria-hidden="true" /></button>
            </div>
          </form>
        </aside>
      </div>
    </div>
  );
}
