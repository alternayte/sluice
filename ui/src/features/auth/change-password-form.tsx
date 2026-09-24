import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { changePasswordMutation } from "@/api/@tanstack/react-query.gen";
import { Button } from "@/components/ui/button";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { SettingsActions, SettingsRow } from "@/components/ui/settings-group";
import { errorMessage, fieldErrors } from "@/lib/errors";

/**
 * ChangePasswordForm posts to /api/v1/auth/password. The rows layout fills a settings group;
 * the sheet layout stacks the fields in a sign-in sheet.
 */
export function ChangePasswordForm({
  onSuccess,
  submitLabel = "Change password",
  layout = "rows",
}: {
  onSuccess?: () => void;
  submitLabel?: string;
  layout?: "rows" | "sheet";
}) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [mismatch, setMismatch] = useState(false);

  const mutation = useMutation({
    ...changePasswordMutation(),
    onSuccess: () => {
      setCurrent("");
      setNext("");
      setConfirm("");
      toast({ title: "Password changed", description: "Other sessions are signed out." });
      onSuccess?.();
    },
  });

  const fields = fieldErrors(mutation.error);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (next !== confirm) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    mutation.mutate({ body: { current_password: current, new_password: next } });
  };

  const sheet = layout === "sheet";
  const Row = sheet ? Field : SettingsRow;
  const submitButton = (
    <Button type="submit" className={sheet ? "w-full" : undefined} disabled={mutation.isPending}>
      {mutation.isPending ? "Saving" : submitLabel}
    </Button>
  );

  return (
    <form onSubmit={submit} className={sheet ? "flex flex-col gap-3" : "divide-y"}>
      <Row id="pw-current" label="Current password" error={fields.current_password}>
        <Input
          type="password"
          autoComplete="current-password"
          required
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
      </Row>
      <Row id="pw-new" label="New password" hint="Use at least 10 characters." error={fields.new_password}>
        <Input
          type="password"
          autoComplete="new-password"
          required
          minLength={10}
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
      </Row>
      <Row id="pw-confirm" label="Confirm new password" error={mismatch ? "The passwords do not match." : undefined}>
        <Input
          type="password"
          autoComplete="new-password"
          required
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
      </Row>
      {sheet ? (
        <>
          {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
          <div className="mt-1">{submitButton}</div>
        </>
      ) : (
        <SettingsActions className={mutation.isError ? "justify-between" : undefined}>
          {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
          {submitButton}
        </SettingsActions>
      )}
    </form>
  );
}
