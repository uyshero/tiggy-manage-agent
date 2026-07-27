library(dplyr)
library(readr)
library(stringi)
library(stringr)

clean_followup_data <- function(raw) {
  raw |>
    rename(
      patient_id = `患者编号`,
      followup_raw = `随访时间（月）`,
      event_raw = `结局状态`,
      treatment_raw = `治疗方案`,
      age_raw = `年龄`,
      stage_raw = `疾病分期`
    ) |>
    mutate(
      across(where(is.character), ~ str_squish(stri_trans_nfkc(.x))),
      followup_month = parse_number(followup_raw),
      age = parse_number(age_raw),
      event = case_when(
        event_raw %in% c("死亡", "已死亡", "事件发生") ~ 1L,
        event_raw %in% c("存活", "失访", "删失", "未发生") ~ 0L,
        TRUE ~ NA_integer_
      ),
      treatment = case_when(
        treatment_raw %in% c("新方案", "新药组", "试验组") ~ "new",
        treatment_raw %in% c("常规治疗", "标准组", "对照组") ~ "standard",
        TRUE ~ NA_character_
      ),
      stage = case_when(
        stage_raw %in% c("II", "II期", "2", "2期") ~ "II",
        stage_raw %in% c("III", "III期", "3", "3期") ~ "III",
        stage_raw %in% c("IV", "IV期", "4", "4期") ~ "IV",
        TRUE ~ NA_character_
      )
    )
}
