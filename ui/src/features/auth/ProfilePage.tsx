import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { revokeOtherSessionsMutation, updateMeMutation } from "@/api/@tanstack/react-query.gen";
import { ChangePasswordForm } from "@/features/auth/change-password-form";
import { themes, useTheme, type Theme } from "@/lib/theme";
import { Button } from "@/components/ui/button";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { meQueryKey, useCurrentUser } from "@/lib/auth";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { roleLabels } from "@/lib/roles";

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex max-w-lg flex-col gap-3 rounded-[8px] border bg-panel p-4">
      <h2 className="text-sm font-semibold">{title}</h2>
      {children}
    </section>
  );
}

export function ProfilePage() {
  const me = useCurrentUser();
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Profile" description={`${me.email} · ${roleLabels[me.role]}`} />
      <Section title="Name">
        <NameForm />
      </Section>
      <Section title="Password">
        <ChangePasswordForm />
      </Section>
      <Section title="Sessions">
        <RevokeOthers />
      </Section>
      <Section title="Theme">
        <ThemeChoice />
      </Section>
    </div>
  );
}

function NameForm() {
  const me = useCurrentUser();
  const qc = useQueryClient();
  const [name, setName] = useState(me.name);
  const [saved, setSaved] = useState(false);
  const mutation = useMutation({
    ...updateMeMutation(),
    onSuccess: (data) => {
      qc.setQueryData(meQueryKey(), data);
      setSaved(true);
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setSaved(false);
    mutation.mutate({ body: { name } });
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field id="profile-name" label="Name" error={fieldErrors(mutation.error).name}>
        <Input maxLength={200} value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      {saved && <p className="text-sm text-state-success">Name saved.</p>}
      <div>
        <Button type="submit" disabled={mutation.isPending || name === me.name}>
          Save name
        </Button>
      </div>
    </form>
  );
}

function RevokeOthers() {
  const mutation = useMutation(revokeOtherSessionsMutation());
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-muted-foreground">Sign out all other browsers and devices. This session stays signed in.</p>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      {mutation.isSuccess && (
        <p className="text-sm text-state-success">
          {mutation.data.count === 1 ? "Signed out 1 other session." : `Signed out ${mutation.data.count} other sessions.`}
        </p>
      )}
      <div>
        <Button variant="secondary" disabled={mutation.isPending} onClick={() => mutation.mutate({})}>
          Sign out other sessions
        </Button>
      </div>
    </div>
  );
}

function ThemeChoice() {
  const { theme, setTheme } = useTheme();
  return (
    <Field id="profile-theme" label="Theme">
      <Select value={theme} onChange={(e) => setTheme(e.target.value as Theme)}>
        {themes.map((t) => (
          <option key={t.value} value={t.value}>
            {t.label}
          </option>
        ))}
      </Select>
    </Field>
  );
}
