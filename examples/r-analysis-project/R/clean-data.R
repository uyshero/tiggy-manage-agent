# 规则来源：skills/r-survival-data-cleaning/SKILL.md
# 说明：这是可运行的起步脚本；智能体应结合原始数据样本、variable-mapping.yml 和 Skill 继续细化取值映射。

raw_path <- "data/raw/随访数据.csv"
raw_path_fallback <- "data/raw/followup.csv"
output_path <- "data/processed/followup_clean.csv"
input_encoding <- Sys.getenv("FOLLOWUP_ENCODING", "UTF-8")

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

squish <- function(x) trimws(gsub("\\s+", " ", as.character(x)))
parse_number <- function(x) suppressWarnings(as.numeric(gsub("[^0-9.+-]", "", as.character(x))))
cat_md <- function(...) cat(..., sep = "")
md_section <- function(title) cat_md("## ", title, "\n\n")
md_bullet <- function(label, value) cat_md("- ", label, "：", value, "\n")
md_values <- function(values) {
  if (!length(values)) return("无")
  paste(sprintf("`%s`", values), collapse = "、")
}
collect_unresolved <- function(raw_values, cleaned_values) {
  pending <- sort(unique(raw_values[(is.na(cleaned_values) | cleaned_values == "") & !is.na(raw_values) & nzchar(raw_values)]))
  pending[nzchar(pending)]
}

patient_id_raw <- as.character(column("患者编号"))
treatment_raw <- squish(column("治疗组"))
event_raw <- squish(column("结局"))
stage_raw <- squish(column("分期"))
sex_raw <- squish(column("性别"))

raw_row_count <- nrow(raw_followup)
followup <- data.frame(
  patient_id = patient_id_raw,
  treatment = ifelse(grepl("新|试验", treatment_raw), "new", ifelse(grepl("标准|对照", treatment_raw), "standard", NA)),
  followup_month = parse_number(column("随访月数")),
  event = ifelse(event_raw %in% c("死亡", "复发", "进展", "1"), 1L, ifelse(event_raw %in% c("存活", "无事件", "失访", "0"), 0L, NA)),
  age = parse_number(column("年龄")),
  stage = ifelse(grepl("IV期|四期|Ⅳ", stage_raw), "IV",
    ifelse(grepl("III期|三期|Ⅲ", stage_raw), "III",
      ifelse(grepl("II期|二期|Ⅱ", stage_raw), "II",
        ifelse(grepl("I期|一期|Ⅰ", stage_raw), "I", NA)))),
  sex = ifelse(grepl("男", sex_raw), "male", ifelse(grepl("女", sex_raw), "female", NA)),
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

event_unresolved <- collect_unresolved(event_raw, as.character(ifelse(event_raw %in% c("死亡", "复发", "进展", "1"), 1L, ifelse(event_raw %in% c("存活", "无事件", "失访", "0"), 0L, NA))))
treatment_unresolved <- collect_unresolved(treatment_raw, ifelse(grepl("新|试验", treatment_raw), "new", ifelse(grepl("标准|对照", treatment_raw), "standard", NA)))
stage_unresolved <- collect_unresolved(stage_raw, ifelse(grepl("IV期|四期|Ⅳ", stage_raw), "IV",
  ifelse(grepl("III期|三期|Ⅲ", stage_raw), "III",
    ifelse(grepl("II期|二期|Ⅱ", stage_raw), "II",
      ifelse(grepl("I期|一期|Ⅰ", stage_raw), "I", NA)))))
sex_unresolved <- collect_unresolved(sex_raw, ifelse(grepl("男", sex_raw), "male", ifelse(grepl("女", sex_raw), "female", NA)))

dir.create("data/processed", recursive = TRUE, showWarnings = FALSE)
write.csv(followup, output_path, row.names = FALSE, fileEncoding = "UTF-8")

cat_md("# 数据清洗执行报告\n\n")
md_section("执行结果")
md_bullet("原始行数", raw_row_count)
md_bullet("输出行数", nrow(followup))
md_bullet("重复编号移除", duplicate_removed)
md_bullet("无效记录移除", invalid_removed)
md_bullet("输出文件", sprintf("`%s`", output_path))
cat_md("\n")

md_section("字段映射")
cat_md("- `patient_id` <- `患者编号`\n")
cat_md("- `treatment` <- `治疗组`\n")
cat_md("- `followup_month` <- `随访月数`\n")
cat_md("- `event` <- `结局`\n")
cat_md("- `age` <- `年龄`\n")
cat_md("- `stage` <- `分期`\n")
cat_md("- `sex` <- `性别`\n\n")

md_section("质量检查")
md_bullet("patient_id_unique", if (anyDuplicated(followup$patient_id) == 0) "通过" else "失败")
md_bullet("followup_month_non_negative", if (all(followup$followup_month >= 0, na.rm = TRUE)) "通过" else "失败")
md_bullet("event_binary_0_1", if (all(followup$event %in% c(0L, 1L), na.rm = TRUE)) "通过" else "失败")
md_bullet("required_survival_fields_not_missing", if (all(!is.na(followup$patient_id) & nzchar(followup$patient_id) & !is.na(followup$followup_month) & !is.na(followup$event))) "通过" else "失败")
md_bullet("缺失 patient_id", sum(missing_patient_id))
md_bullet("缺失 followup_month", sum(missing_followup))
md_bullet("缺失 event", sum(missing_event))
md_bullet("负数随访时间", sum(negative_followup))
cat_md("\n")

md_section("待确认取值")
cat_md("### 结局\n\n", md_values(event_unresolved), "\n\n")
cat_md("### 治疗组\n\n", md_values(treatment_unresolved), "\n\n")
cat_md("### 分期\n\n", md_values(stage_unresolved), "\n\n")
cat_md("### 性别\n\n", md_values(sex_unresolved), "\n\n")

md_section("数据概览")
summary_text <- paste(capture.output(summary(followup)), collapse = "\n")
cat_md("```text\n", summary_text, "\n```\n")
