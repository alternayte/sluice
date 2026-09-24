import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { revokeOtherSessionsMutation, updateMeMutation } from "@/api/@tanstack/react-query.gen";
import { ChangePasswordForm } from "@/features/auth/change-password-form";
import { SettingsActions, SettingsGroup, SettingsRow } from "@/components/ui/settings-group";
import { themes, useTheme } from "@/lib/theme";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { SegmentedControl } from "@/components/ui/segmented";
import { toast } from "@/components/ui/toast";
import { meQueryKey, useCurrentUser } from "@/lib/auth";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { roleLabels } from "@/lib/roles";

export function ProfilePage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Profile" description="Your account, password, sessions and appearance." />
      <div className="flex max-w-2xl flex-col gap-6">
        <AccountGroup />
        <SettingsGroup title="Password">
          <ChangePasswordForm />
        </SettingsGroup>
        <SessionsGroup />
        <SettingsGroup title="Appearance">
          <ThemeChoice />
        </SettingsGroup>
      </div>
    </div>
  );
}

function AccountGroup() {
  const me = useCurrentUser();
  const qc = useQueryClient();
  const [name, setName] = useState(me.name);
  const mutation = useMutation({
    ...updateMeMutation(),
    onSuccess: (data) => {
      qc.setQueryData(meQueryKey(), data);
      toast({ title: "Name saved" });
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    mutation.mutate({ body: { name } });
  };
  return (
    <SettingsGroup title="Account">
      <SettingsRow label="Email">
        <span className="truncate text-sm text-muted-foreground" title={me.email}>
          {me.email}
        </span>
      </SettingsRow>
      <SettingsRow label="Role">
        <span className="text-sm text-muted-foreground">{roleLabels[me.role]}</span>
      </SettingsRow>
      <form onSubmit={submit}>
        <SettingsRow
          id="profile-name"
          label="Name"
          error={fieldErrors(mutation.error).name}
          trailing={
            <Button type="submit" variant="secondary" disabled={mutation.isPending || name === me.name}>
              Save name
            </Button>
          }
        >
          <Input maxLength={200} value={name} onChange={(e) => setName(e.target.value)} />
        </SettingsRow>
        {mutation.isError && (
          <div className="px-4 pb-2.5">
            <FormError>{errorMessage(mutation.error)}</FormError>
          </div>
        )}
      </form>
    </SettingsGroup>
  );
}

function SessionsGroup() {
  const mutation = useMutation({
    ...revokeOtherSessionsMutation(),
    onSuccess: (d) =>
      toast({
        title: d.count === 1 ? "Signed out 1 other session" : `Signed out ${d.count} other sessions`,
      }),
  });
  return (
    <SettingsGroup title="Sessions" footer="Sign out all other browsers and devices. This session stays signed in.">
      <SettingsRow label="Other sessions">
        <Button
          variant="secondary"
          className="ml-auto"
          disabled={mutation.isPending}
          onClick={() => mutation.mutate({})}
        >
          Sign out other sessions
        </Button>
      </SettingsRow>
      {mutation.isError && (
        <SettingsActions className="justify-start">
          <FormError>{errorMessage(mutation.error)}</FormError>
        </SettingsActions>
      )}
    </SettingsGroup>
  );
}

function ThemeChoice() {
  const { theme, setTheme } = useTheme();
  return (
    <SettingsRow label="Theme">
      <SegmentedControl
        label="Theme"
        options={themes.map((t) => ({ value: t.value, label: t.label }))}
        value={theme}
        onChange={setTheme}
      />
    </SettingsRow>
  );
}
