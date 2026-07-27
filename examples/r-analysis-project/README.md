# R Analysis Project

Reproducible R analysis project template used by the TMA Analysis Workbench.

## Structure

- `notebooks/`: exploratory and report notebooks
- `R/`: reusable cleaning and modeling functions
- `config/`: explicit Chinese-field and category mappings
- `data/`: governed inputs; raw and private data are ignored by Git
- `reports/`: Quarto or R Markdown reports
- `outputs/`: generated files, ignored by Git

Restore the declared environment with `renv::restore()` after the GitLab
connector creates the project.
