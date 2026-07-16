import React, { useEffect, useId, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";

const focusableSelector = [
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "a[href]",
  "[tabindex]:not([tabindex='-1'])"
].join(",");

function useDialogRequest(service) {
  const [request, setRequest] = useState(() => service.getActive());
  useEffect(() => service.subscribe(setRequest), [service]);
  return request;
}

function DialogFrame({ children, description, onCancel, request, title }) {
  const dialogRef = useRef(null);
  const onCancelRef = useRef(onCancel);
  const titleID = useId();
  const descriptionID = useId();
  const dismissible = request.options.dismissible !== false;
  onCancelRef.current = onCancel;

  useEffect(() => {
    const previousFocus = document.activeElement;
    const dialog = dialogRef.current;
    document.body.classList.add("workbench-dialog-open");

    const focusFrame = window.requestAnimationFrame(() => {
      const preferred = dialog?.querySelector("[data-dialog-autofocus]");
      const first = preferred || dialog?.querySelector(focusableSelector);
      (first || dialog)?.focus();
    });

    const handleKeyDown = (event) => {
      if (event.key === "Escape" && dismissible) {
        event.preventDefault();
        onCancelRef.current();
        return;
      }
      if (event.key !== "Tab" || !dialog) return;
      const focusable = [...dialog.querySelectorAll(focusableSelector)];
      if (!focusable.length) {
        event.preventDefault();
        dialog.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      window.cancelAnimationFrame(focusFrame);
      document.removeEventListener("keydown", handleKeyDown);
      document.body.classList.remove("workbench-dialog-open");
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus();
    };
  }, [dismissible, request.id]);

  return (
    <div
      className="workbench-dialog-backdrop"
      data-dialog-kind={request.kind}
      onMouseDown={(event) => {
        if (dismissible && event.target === event.currentTarget) onCancel();
      }}
      role="presentation"
    >
      <section
        aria-describedby={description ? descriptionID : undefined}
        aria-labelledby={titleID}
        aria-modal="true"
        className={`workbench-dialog workbench-dialog-${request.kind}`}
        ref={dialogRef}
        role={request.kind === "confirm" && request.options.tone === "danger" ? "alertdialog" : "dialog"}
        tabIndex={-1}
      >
        <header className="workbench-dialog-header">
          <div>
            <h2 id={titleID}>{title}</h2>
            {description ? <p id={descriptionID}>{description}</p> : null}
          </div>
          {dismissible ? (
            <button className="workbench-dialog-close" type="button" aria-label="关闭" title="关闭" onClick={onCancel}>×</button>
          ) : null}
        </header>
        {children}
      </section>
    </div>
  );
}

function ConfirmDialog({ request, service }) {
  const { options } = request;
  return (
    <DialogFrame
      description={options.description}
      onCancel={() => service.cancel(request.id)}
      request={request}
      title={options.title}
    >
      {options.detail ? <div className="workbench-dialog-detail">{options.detail}</div> : null}
      <footer className="workbench-dialog-actions">
        <button className="secondary" type="button" onClick={() => service.cancel(request.id)}>{options.cancelLabel}</button>
        <button
          className={`workbench-dialog-confirm ${options.tone}`}
          data-dialog-autofocus
          type="button"
          onClick={() => service.resolve(request.id, true)}
        >
          {options.confirmLabel}
        </button>
      </footer>
    </DialogFrame>
  );
}

function fieldInitialValue(schema, value) {
  if (value !== undefined) return value;
  if (schema.default !== undefined) return schema.default;
  if (schema.type === "boolean") return false;
  return "";
}

function FormDialog({ request, service }) {
  const { options } = request;
  const properties = options.schema.properties || {};
  const required = new Set(options.schema.required || []);
  const [values, setValues] = useState(() => Object.fromEntries(
    Object.entries(properties).map(([name, schema]) => [name, fieldInitialValue(schema, options.initialValues[name])])
  ));

  return (
    <DialogFrame
      description={options.description}
      onCancel={() => service.cancel(request.id)}
      request={request}
      title={options.title}
    >
      <form
        className="workbench-dialog-form"
        onSubmit={(event) => {
          event.preventDefault();
          service.resolve(request.id, values);
        }}
      >
        <div className="workbench-dialog-fields">
          {Object.entries(properties).map(([name, schema], index) => {
            const label = schema.title || name;
            const shared = {
              id: `dialog-field-${request.id}-${name}`,
              name,
              required: required.has(name),
              value: values[name],
              onChange: (event) => {
                const next = schema.type === "boolean" ? event.target.checked : event.target.value;
                setValues((current) => ({ ...current, [name]: next }));
              }
            };
            return (
              <label className={`workbench-dialog-field ${schema.type === "boolean" ? "boolean" : ""}`} key={name} htmlFor={shared.id}>
                <span>{label}{required.has(name) ? " *" : ""}</span>
                {Array.isArray(schema.enum) ? (
                  <select {...shared} data-dialog-autofocus={index === 0 ? "true" : undefined}>
                    {schema.enum.map((option) => <option key={String(option)} value={option}>{String(option)}</option>)}
                  </select>
                ) : schema.type === "boolean" ? (
                  <input {...shared} checked={Boolean(values[name])} type="checkbox" value={undefined} />
                ) : schema.format === "textarea" ? (
                  <textarea {...shared} data-dialog-autofocus={index === 0 ? "true" : undefined} placeholder={schema.description || ""} />
                ) : (
                  <input
                    {...shared}
                    data-dialog-autofocus={index === 0 ? "true" : undefined}
                    placeholder={schema.description || ""}
                    type={["integer", "number"].includes(schema.type) ? "number" : "text"}
                  />
                )}
              </label>
            );
          })}
        </div>
        <footer className="workbench-dialog-actions">
          <button className="secondary" type="button" onClick={() => service.cancel(request.id)}>{options.cancelLabel}</button>
          <button type="submit">{options.submitLabel}</button>
        </footer>
      </form>
    </DialogFrame>
  );
}

function ChoiceDialog({ request, service }) {
  const { options } = request;
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(options.initialValue);
  const filteredItems = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    if (!normalized) return options.items;
    return options.items.filter((item) => (
      `${item.label}\n${item.description}\n${item.keywords}`.toLocaleLowerCase().includes(normalized)
    ));
  }, [options.items, query]);

  useEffect(() => {
    if (filteredItems.some((item) => item.value === selected && !item.disabled)) return;
    setSelected(filteredItems.find((item) => !item.disabled)?.value || "");
  }, [filteredItems, selected]);

  return (
    <DialogFrame
      description={options.description}
      onCancel={() => service.cancel(request.id)}
      request={request}
      title={options.title}
    >
      <form
        className="workbench-dialog-choice-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (selected) service.resolve(request.id, selected);
        }}
      >
        {options.searchable ? (
          <div className="workbench-dialog-choice-search">
            <input
              aria-label="搜索选项"
              data-dialog-autofocus
              onChange={(event) => setQuery(event.target.value)}
              placeholder={options.searchPlaceholder}
              type="search"
              value={query}
            />
          </div>
        ) : null}
        <div aria-label={options.title} className="workbench-dialog-choice-list" role="radiogroup">
          {filteredItems.length ? filteredItems.map((item, index) => (
            <label className={`workbench-dialog-choice-item ${selected === item.value ? "selected" : ""} ${item.disabled ? "disabled" : ""}`} key={item.value}>
              <input
                checked={selected === item.value}
                data-dialog-autofocus={!options.searchable && index === 0 ? "true" : undefined}
                disabled={item.disabled}
                name={`choice-${request.id}`}
                onChange={() => setSelected(item.value)}
                type="radio"
                value={item.value}
              />
              <span>
                <strong>{item.label}</strong>
                {item.description ? <small>{item.description}</small> : null}
              </span>
            </label>
          )) : <div className="workbench-dialog-choice-empty">{options.emptyMessage}</div>}
        </div>
        <footer className="workbench-dialog-actions">
          <button className="secondary" type="button" onClick={() => service.cancel(request.id)}>{options.cancelLabel}</button>
          <button disabled={!selected} type="submit">{options.submitLabel}</button>
        </footer>
      </form>
    </DialogFrame>
  );
}

function CustomDialog({ request, service }) {
  const Renderer = request.options.renderer;
  return (
    <DialogFrame
      description={request.options.description}
      onCancel={() => service.cancel(request.id)}
      request={request}
      title={request.options.title}
    >
      <div className="workbench-dialog-custom">
        <Renderer
          cancel={() => service.cancel(request.id)}
          close={(result) => service.resolve(request.id, result)}
          input={request.options.input}
        />
      </div>
    </DialogFrame>
  );
}

export default function DialogHost({ service }) {
  const request = useDialogRequest(service);
  if (!request) return null;

  let dialog = null;
  if (request.kind === "confirm") dialog = <ConfirmDialog request={request} service={service} />;
  if (request.kind === "form") dialog = <FormDialog key={request.id} request={request} service={service} />;
  if (request.kind === "choice") dialog = <ChoiceDialog key={request.id} request={request} service={service} />;
  if (request.kind === "custom") dialog = <CustomDialog request={request} service={service} />;
  return dialog ? createPortal(dialog, document.body) : null;
}
