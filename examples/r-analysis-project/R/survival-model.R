library(ggsurvfit)
library(survival)

fit_survival_models <- function(followup) {
  survival_object <- with(followup, Surv(followup_month, event))
  list(
    kaplan_meier = survfit(survival_object ~ treatment, data = followup),
    cox = coxph(survival_object ~ treatment + age + stage, data = followup)
  )
}

plot_kaplan_meier <- function(model) {
  ggsurvfit(model) + add_risktable()
}
