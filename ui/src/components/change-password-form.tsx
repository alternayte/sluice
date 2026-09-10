import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, unwrap } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { errorMessage, fieldErrors } from "@/lib/errors";

/** ChangePasswordForm posts to /api/v1/auth/password. */
export function ChangePasswordForm({ onSuccess, submitLabel = "Change password" }: { onSuccess?: () => void; submitLabel?: string }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [mismatch, setMismatch] = useState(false);
  const [done, setDone] = useState(false);

  const mutation = useMutation({
    mutationFn: async () =>
      unwrap(await api.POST("/api/v1/auth/password", { body: { current_password: current, new_password: next } })),
    onSuccess: () => {
      setCurrent("");
      setNext("");
      setConfirm("");
      setDone(true);
      onSuccess?.();
    },
  });

  const fields = fieldErrors(mutation.error);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setDone(false);
    if (next !== confirm) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    mutation.mutate();
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field id="pw-current" label="Current password" error={fields.current_password}>
        <Input type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
      </Field>
      <Field id="pw-new" label="New password" hint="Use at least 10 characters." error={fields.new_password}>
        <Input type="password" autoComplete="new-password" required minLength={10} value={next} onChange={(e) => setNext(e.target.value)} />
      </Field>
      <Field id="pw-confirm" label="Confirm new password" error={mismatch ? "The passwords do not match." : undefined}>
        <Input type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      </Field>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      {done && <p className="text-sm text-state-success">Password changed. Other sessions are signed out.</p>}
      <div>
        <Button type="submit" disabled={mutation.isPending}>
          {mutation.isPending ? "Saving" : submitLabel}
        </Button>
      </div>
    </form>
  );
}
