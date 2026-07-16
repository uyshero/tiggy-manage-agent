export function objectRecord(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

export function selectedChoiceIDs(value, choices = []) {
  const selected = objectRecord(value);
  const orderedChoices = Array.isArray(choices) ? choices : [];
  return orderedChoices.filter((choice) => Boolean(selected[choice.id])).map((choice) => choice.id);
}

export function fieldValue(field, answer) {
  const value = objectRecord(answer)[field.id];
  if (field.type === "multiselect") return selectedChoiceIDs(value, field.choices || []);
  return value ?? "";
}

export function fieldHasValue(field, answer) {
  const value = fieldValue(field, answer);
  if (Array.isArray(value)) return value.length > 0;
  return String(value || "").trim() !== "";
}

export function buildHumanInputResponse(mode, choices, fields, answer) {
  const normalizedMode = String(mode || "freeform");
  if (normalizedMode === "multiselect") {
    return { mode: normalizedMode, answers: selectedChoiceIDs(answer, choices) };
  }
  if (normalizedMode === "form") {
    const values = {};
    for (const field of Array.isArray(fields) ? fields : []) {
      values[field.id] = fieldValue(field, answer);
    }
    return { mode: normalizedMode, fields: values };
  }
  return { mode: normalizedMode, answer };
}

export function canSubmitHumanInput(mode, choices, fields, answer) {
  const response = buildHumanInputResponse(mode, choices, fields, answer);
  if (mode === "multiselect") return response.answers.length > 0;
  if (mode === "form") {
    return (Array.isArray(fields) ? fields : []).every((field) => !field.required || fieldHasValue(field, answer));
  }
  return String(response.answer || "").trim() !== "";
}
