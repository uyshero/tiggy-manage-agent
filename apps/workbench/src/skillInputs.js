const hasOwn = (value, key) => Object.prototype.hasOwnProperty.call(value || {}, key);

export class SkillInputsValidationError extends Error {
  constructor(fields) {
    super("请检查 Skill 参数。");
    this.name = "SkillInputsValidationError";
    this.fields = fields;
  }
}

function parseObject(value) {
  if (!value) return null;
  if (typeof value === "string") {
    try {
      return parseObject(JSON.parse(value));
    } catch {
      return null;
    }
  }
  return typeof value === "object" && !Array.isArray(value) ? value : null;
}

export function inputSchemaFromVersion(version) {
  const manifest = parseObject(version?.manifest);
  return parseObject(manifest?.inputs_schema);
}

export function skillBindingsFromConfig(raw) {
  let source = raw;
  if (typeof source === "string") {
    try {
      source = JSON.parse(source);
    } catch {
      return [];
    }
  }
  if (!source || typeof source !== "object" || Array.isArray(source) || !Array.isArray(source.enabled)) return [];
  return source.enabled.flatMap((item) => {
    if (!item || typeof item !== "object" || Array.isArray(item)) return [];
    const skill = String(item.skill || "").trim();
    const version = Number(item.version || 0);
    if (!skill || !Number.isInteger(version) || version <= 0) return [];
    const priority = Number(item.priority);
    return [{
      skill,
      version,
      mode: String(item.mode || "summary").trim() || "summary",
      priority: Number.isInteger(priority) && priority !== 0 ? priority : 100,
      inputs: parseObject(item.inputs) || {}
    }];
  });
}

function canonicalJSON(value) {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

export function skillBindingsMatch(left, right) {
  if (!left || !right) return left === right;
  return left.skill === right.skill
    && left.version === right.version
    && left.mode === right.mode
    && left.priority === right.priority
    && canonicalJSON(left.inputs || {}) === canonicalJSON(right.inputs || {});
}

export function skillBindingState(agentBinding, sessionBinding) {
  const synced = skillBindingsMatch(agentBinding, sessionBinding);
  return {
    synced,
    pendingApply: Boolean(agentBinding) && !synced,
    sessionStillEnabled: !agentBinding && Boolean(sessionBinding)
  };
}

export function skillConfigSyncState(agentConfigVersion, sessionConfigVersion, hasSession = true) {
  const agentVersion = Number(agentConfigVersion || 0);
  const sessionVersion = Number(sessionConfigVersion || 0);
  const needsApply = Boolean(hasSession) && agentVersion > sessionVersion;
  return {
    agentVersion,
    sessionVersion,
    needsApply,
    targetVersion: needsApply ? agentVersion : 0
  };
}

function resolveLocalSchema(root, schema, seen = new Set()) {
  if (!schema || typeof schema !== "object" || Array.isArray(schema) || typeof schema.$ref !== "string") return schema;
  if (!schema.$ref.startsWith("#/") || seen.has(schema.$ref)) return schema;
  let resolved = root;
  for (const token of schema.$ref.slice(2).split("/")) {
    const key = token.replace(/~1/g, "/").replace(/~0/g, "~");
    resolved = resolved?.[key];
  }
  if (!resolved || typeof resolved !== "object" || Array.isArray(resolved)) return schema;
  const nextSeen = new Set(seen);
  nextSeen.add(schema.$ref);
  return { ...resolveLocalSchema(root, resolved, nextSeen), ...schema, $ref: undefined };
}

function schemaType(schema) {
  if (typeof schema?.type === "string") return schema.type;
  if (Array.isArray(schema?.type)) return schema.type.find((value) => value !== "null") || "json";
  if (Array.isArray(schema?.enum) && schema.enum.length) {
    const value = schema.enum.find((item) => item !== null);
    if (Array.isArray(value)) return "array";
    if (value !== null && typeof value === "object") return "object";
    return typeof value;
  }
  return "json";
}

function enumToken(value) {
  return JSON.stringify(value);
}

function enumLabel(value) {
  if (typeof value === "string") return value;
  if (value === null) return "null";
  return JSON.stringify(value);
}

function fieldControl(schema, type) {
  if (Array.isArray(schema.enum)) return "enum";
  if (type === "boolean") return "boolean";
  if (type === "integer" || type === "number") return "number";
  if (type === "object" || type === "array" || type === "json") return "json";
  if (schema["x-tma-control"] === "textarea") return "textarea";
  return "text";
}

export function schemaFields(schema) {
  const root = parseObject(schema);
  if (!root) return [];
  const properties = parseObject(root.properties) || {};
  const required = new Set(Array.isArray(root.required) ? root.required : []);
  return Object.entries(properties).map(([name, unresolved]) => {
    const property = resolveLocalSchema(root, parseObject(unresolved) || {});
    const type = schemaType(property);
    return {
      name,
      title: typeof property.title === "string" && property.title.trim() ? property.title : name,
      description: typeof property.description === "string" ? property.description : "",
      required: required.has(name),
      type,
      control: fieldControl(property, type),
      defaultValue: hasOwn(property, "default") ? property.default : undefined,
      options: Array.isArray(property.enum)
        ? property.enum.map((value) => ({ value, token: enumToken(value), label: enumLabel(value) }))
        : [],
      minimum: typeof property.minimum === "number" ? property.minimum : undefined,
      maximum: typeof property.maximum === "number" ? property.maximum : undefined,
      minLength: Number.isInteger(property.minLength) ? property.minLength : undefined,
      maxLength: Number.isInteger(property.maxLength) ? property.maxLength : undefined
    };
  });
}

function valueForControl(field, value) {
  if (field.control === "enum") {
    const token = enumToken(value);
    return field.options.some((option) => option.token === token) ? token : undefined;
  }
  if (field.control === "boolean") return typeof value === "boolean" ? value : undefined;
  if (field.control === "json") return JSON.stringify(value, null, 2);
  return value === null || value === undefined ? undefined : String(value);
}

export function initialSkillInputValues(schema, inputs) {
  const inputValues = parseObject(inputs);
  const entries = [];
  for (const field of schemaFields(schema)) {
    let value;
    if (inputValues && hasOwn(inputValues, field.name)) value = valueForControl(field, inputValues[field.name]);
    else if (field.defaultValue !== undefined) value = valueForControl(field, field.defaultValue);
    else if (field.required && field.control === "boolean") value = false;
    if (value !== undefined) entries.push([field.name, value]);
  }
  return Object.fromEntries(entries);
}

function isBlank(value) {
  return value === undefined || value === null || (typeof value === "string" && value.trim() === "");
}

function validateJSONType(field, value) {
  if (field.type === "array" && !Array.isArray(value)) return "必须是 JSON 数组";
  if (field.type === "object" && (!value || typeof value !== "object" || Array.isArray(value))) return "必须是 JSON 对象";
  return "";
}

export function buildSkillInputs(schema, values = {}) {
  const inputs = Object.create(null);
  const errors = {};
  for (const field of schemaFields(schema)) {
    const present = hasOwn(values, field.name) && !isBlank(values[field.name]);
    if (!present) {
      if (field.required) errors[field.name] = "此参数为必填项";
      continue;
    }
    const raw = values[field.name];
    if (field.control === "enum") {
      const option = field.options.find((item) => item.token === raw);
      if (!option) errors[field.name] = "请选择有效选项";
      else inputs[field.name] = option.value;
      continue;
    }
    if (field.control === "boolean") {
      if (typeof raw !== "boolean") errors[field.name] = "必须是布尔值";
      else inputs[field.name] = raw;
      continue;
    }
    if (field.control === "number") {
      const parsed = Number(raw);
      if (!Number.isFinite(parsed) || (field.type === "integer" && !Number.isInteger(parsed))) {
        errors[field.name] = field.type === "integer" ? "必须是整数" : "必须是数字";
      } else if (field.minimum !== undefined && parsed < field.minimum) {
        errors[field.name] = `不能小于 ${field.minimum}`;
      } else if (field.maximum !== undefined && parsed > field.maximum) {
        errors[field.name] = `不能大于 ${field.maximum}`;
      } else {
        inputs[field.name] = parsed;
      }
      continue;
    }
    if (field.control === "json") {
      try {
        const parsed = typeof raw === "string" ? JSON.parse(raw) : raw;
        const typeError = validateJSONType(field, parsed);
        if (typeError) errors[field.name] = typeError;
        else inputs[field.name] = parsed;
      } catch {
        errors[field.name] = "请输入有效 JSON";
      }
      continue;
    }
    const parsed = String(raw);
    if (field.minLength !== undefined && parsed.length < field.minLength) errors[field.name] = `至少需要 ${field.minLength} 个字符`;
    else if (field.maxLength !== undefined && parsed.length > field.maxLength) errors[field.name] = `最多允许 ${field.maxLength} 个字符`;
    else inputs[field.name] = parsed;
  }
  if (Object.keys(errors).length) throw new SkillInputsValidationError(errors);
  return inputs;
}
