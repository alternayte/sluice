import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { runFileMutation, triggerFlowMutation } from "@/api/@tanstack/react-query.gen";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { errorMessage, fieldErrors } from "@/lib/errors";
import {
  coerceInputs,
  flowInputs,
  initialFormValue,
  inputFieldErrors,
  parseArgs,
  parseLabelLines,
  type FlowInput,
} from "@/lib/executions";

const textarea =
  "min-h-24 w-full min-w-0 rounded-[6px] border border-input bg-background px-3 py-2 font-mono text-sm text-foreground aria-[invalid=true]:border-destructive";

function InputControl({
  input,
  value,
  onChange,
  error,
}: {
  input: FlowInput;
  value: string | boolean;
  onChange: (v: string | boolean) => void;
  error?: string;
}) {
  const id = `run-input-${input.id}`;
  const label = input.required && (input.default === undefined || input.default === null) ? `${input.id} *` : input.id;
  const hint = input.description || undefined;
  if (input.type === "boolean") {
    return (
      <div className="flex flex-col gap-1">
        <label className="flex items-center gap-2 text-sm font-medium">
          <input
            id={id}
            type="checkbox"
            className="h-4 w-4"
            checked={value === true}
            onChange={(e) => onChange(e.target.checked)}
          />
          {input.id}
        </label>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
    );
  }
  const text = typeof value === "string" ? value : "";
  return (
    <Field id={id} label={label} hint={hint} error={error}>
      {input.type === "select" ? (
        <Select value={text} onChange={(e) => onChange(e.target.value)}>
          <option value="">Select a value</option>
          {(input.values ?? []).map((v) => (
            <option key={String(v)} value={String(v)}>
              {String(v)}
            </option>
          ))}
        </Select>
      ) : input.type === "json" ? (
        <textarea className={textarea} value={text} onChange={(e) => onChange(e.target.value)} spellCheck={false} />
      ) : (
        <Input
          type={input.type === "int" || input.type === "number" ? "number" : "text"}
          step={input.type === "int" ? 1 : input.type === "number" ? "any" : undefined}
          value={text}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </Field>
  );
}

/** RunFlowDialog renders a run form from the flow inputs and starts an execution (REQ-FLOW-008). */
export function RunFlowDialog({
  namespace,
  flowId,
  definition,
  onClose,
}: {
  namespace: string;
  flowId: string;
  definition: Record<string, unknown> | null | undefined;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const inputs = flowInputs(definition);
  const [values, setValues] = useState<Record<string, string | boolean>>(() =>
    Object.fromEntries(inputs.map((i) => [i.id, initialFormValue(i)])),
  );
  const [labelsText, setLabelsText] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [labelsError, setLabelsError] = useState<string>();

  const trigger = useMutation({
    ...triggerFlowMutation(),
    onSuccess: (e) => void navigate({ to: "/executions/$executionId", params: { executionId: e.id } }),
    onError: (err) => {
      const { byInput } = inputFieldErrors(fieldErrors(err));
      setErrors(byInput);
      const f = fieldErrors(err);
      const labelMsg = Object.entries(f).find(([k]) => k === "labels" || k.startsWith("labels."));
      setLabelsError(labelMsg?.[1]);
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const coerced = coerceInputs(inputs, values);
    const labels = parseLabelLines(labelsText);
    setErrors(coerced.errors);
    setLabelsError(labels.error);
    if (Object.keys(coerced.errors).length || labels.error) return;
    trigger.mutate({ path: { namespace, flowId }, body: { inputs: coerced.inputs, labels: labels.labels } });
  };

  const serverOther = trigger.isError ? inputFieldErrors(fieldErrors(trigger.error)).other : [];
  const shownOther = serverOther.filter((m) => !m.startsWith("labels"));

  return (
    <Dialog open onClose={onClose} title={`Run ${flowId}`}>
      <form onSubmit={submit} noValidate className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto">
        {inputs.length === 0 && <p className="text-sm text-muted-foreground">This flow has no inputs.</p>}
        {inputs.map((input) => (
          <InputControl
            key={input.id}
            input={input}
            value={values[input.id] ?? ""}
            error={errors[input.id]}
            onChange={(v) => setValues((prev) => ({ ...prev, [input.id]: v }))}
          />
        ))}
        <Field id="run-labels" label="Labels" hint="Optional. One key=value per line." error={labelsError}>
          <textarea className={textarea} value={labelsText} onChange={(e) => setLabelsText(e.target.value)} />
        </Field>
        {trigger.isError && (shownOther.length > 0 || Object.keys(fieldErrors(trigger.error)).length === 0) && (
          <FormError>{shownOther.length ? shownOther.join(" ") : errorMessage(trigger.error)}</FormError>
        )}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={trigger.isPending}>
            {trigger.isPending ? "Starting" : "Run"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/** RunFileDialog runs a namespace script file with arguments (REQ-NS-007). */
export function RunFileDialog({ namespace, path, onClose }: { namespace: string; path: string; onClose: () => void }) {
  const navigate = useNavigate();
  const [args, setArgs] = useState("");
  const run = useMutation({
    ...runFileMutation(),
    onSuccess: (e) => void navigate({ to: "/executions/$executionId", params: { executionId: e.id } }),
  });
  const fields = run.isError ? fieldErrors(run.error) : {};
  const argsError = Object.entries(fields).find(([k]) => k.startsWith("args"))?.[1];

  return (
    <Dialog open onClose={onClose} title={`Run ${path}`}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          run.mutate({ path: { namespace }, body: { path, args: parseArgs(args) } });
        }}
        className="flex flex-col gap-4"
      >
        <Field id="run-args" label="Arguments" hint="Optional. One argument per line." error={argsError}>
          <textarea className={textarea} value={args} onChange={(e) => setArgs(e.target.value)} autoFocus />
        </Field>
        {run.isError && !argsError && <FormError>{errorMessage(run.error)}</FormError>}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={run.isPending}>
            {run.isPending ? "Starting" : "Run"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
