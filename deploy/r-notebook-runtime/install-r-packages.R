options(repos = c(CRAN = "https://cloud.r-project.org"), Ncpus = max(1L, parallel::detectCores() - 1L))

packages <- c(
  "IRkernel",
  "renv",
  "jsonlite",
  "readr",
  "readxl",
  "openxlsx",
  "dplyr",
  "tidyr",
  "stringr",
  "stringi",
  "lubridate",
  "janitor",
  "ggplot2",
  "survival",
  "broom",
  "gtsummary",
  "ggsurvfit"
)

missing <- packages[!vapply(packages, requireNamespace, quietly = TRUE, FUN.VALUE = logical(1))]
if (length(missing)) install.packages(missing)

IRkernel::installspec(user = FALSE, name = "ir45", displayname = "R 4.5")
