import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Ban, CheckCircle2, KeyRound, Plus, Users } from "lucide-react";
import { useState, type FormEvent } from "react";
import {
  createUserMutation,
  listUsersInfiniteOptions,
  resetUserPasswordMutation,
  updateUserMutation,
} from "@/api/@tanstack/react-query.gen";
import type { User } from "@/api/types.gen";
import { DataState } from "@/components/data-state";
import { LoadMore } from "@/components/load-more";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { toast } from "@/components/ui/toast";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { roleLabels, roles, type Role } from "@/lib/roles";
import { formatTime } from "@/lib/utils";

const usersQueryKey = [{ _id: "listUsers" }];

export function UsersPage() {
  const me = useCurrentUser();
  const qc = useQueryClient();
  const [creating, setCreating] = useState(false);
  const [resetting, setResetting] = useState<User | null>(null);

  const users = useInfiniteQuery({
    ...listUsersInfiniteOptions(),
    initialPageParam: {},
    getNextPageParam: (last) => (last.next_cursor ? { query: { cursor: last.next_cursor } } : undefined),
    select: (d) => d.pages.flatMap((p) => p.items),
  });

  const update = useMutation({
    ...updateUserMutation(),
    onSuccess: (u, vars) => {
      if (vars.body.role !== undefined)
        toast({ title: "Role changed", description: `${u.email} is now ${roleLabels[u.role]}.` });
      else toast({ title: u.disabled ? "User disabled" : "User enabled", description: u.email });
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: usersQueryKey }),
  });

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Users"
        description="People who can sign in to Sluice."
        actions={
          <Button onClick={() => setCreating(true)}>
            <Plus className="h-4 w-4" aria-hidden />
            Create user
          </Button>
        }
      />
      {update.isError && <FormError>{errorMessage(update.error)}</FormError>}
      <DataState query={users} empty={(d) => d.length === 0} emptyText="No users exist." emptyIcon={Users}>
        {(items) => (
          <>
            <Table>
              <THead>
                <Tr>
                  <Th>Email</Th>
                  <Th>Name</Th>
                  <Th>Role</Th>
                  <Th>State</Th>
                  <Th>Last login</Th>
                  <Th className="relative">
                    <span className="sr-only">Actions</span>
                  </Th>
                </Tr>
              </THead>
              <TBody>
                {items.map((u) => (
                  <Tr key={u.id}>
                    <Td>
                      <div className="flex items-center gap-2.5">
                        <span
                          aria-hidden
                          className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground"
                        >
                          {(u.name || u.email).slice(0, 1).toUpperCase()}
                        </span>
                        <span className="font-medium">{u.email}</span>
                        {u.id === me.id && <span className="text-xs text-muted-foreground">(you)</span>}
                      </div>
                    </Td>
                    <Td className={u.name ? undefined : "text-muted-foreground"}>{u.name || "—"}</Td>
                    <Td>
                      <Select
                        aria-label={`Role of ${u.email}`}
                        className="h-7 w-32 text-xs"
                        value={u.role}
                        disabled={update.isPending}
                        onChange={(e) =>
                          update.mutate({ path: { userId: u.id }, body: { role: e.target.value as Role } })
                        }
                      >
                        {roles.map((r) => (
                          <option key={r} value={r}>
                            {roleLabels[r]}
                          </option>
                        ))}
                      </Select>
                    </Td>
                    <Td>
                      <div className="flex items-center gap-3">
                        {u.disabled ? (
                          <Badge tone="failed" icon={Ban}>
                            Disabled
                          </Badge>
                        ) : (
                          <Badge tone="success" icon={CheckCircle2}>
                            Enabled
                          </Badge>
                        )}
                        {u.must_change_password && (
                          <Badge tone="warning" icon={KeyRound}>
                            Temporary password
                          </Badge>
                        )}
                      </div>
                    </Td>
                    <Td className="text-muted-foreground tabular-nums">
                      {u.last_login_at ? formatTime(u.last_login_at) : "Never"}
                    </Td>
                    <Td className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={update.isPending}
                          onClick={() => update.mutate({ path: { userId: u.id }, body: { disabled: !u.disabled } })}
                        >
                          {u.disabled ? "Enable" : "Disable"}
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setResetting(u)}>
                          Reset password
                        </Button>
                      </div>
                    </Td>
                  </Tr>
                ))}
              </TBody>
            </Table>
            <LoadMore query={users} />
          </>
        )}
      </DataState>
      <Dialog open={creating} onClose={() => setCreating(false)} title="Create user">
        <CreateUserForm onDone={() => setCreating(false)} />
      </Dialog>
      <Dialog open={resetting !== null} onClose={() => setResetting(null)} title="Reset password">
        {resetting && <ResetPasswordForm user={resetting} onDone={() => setResetting(null)} />}
      </Dialog>
    </div>
  );
}

function CreateUserForm({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const [password, setPassword] = useState("");
  const mutation = useMutation({
    ...createUserMutation(),
    onSuccess: (u) => {
      void qc.invalidateQueries({ queryKey: usersQueryKey });
      toast({ title: "User created", description: u.email });
      onDone();
    },
  });
  const fields = fieldErrors(mutation.error);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    mutation.mutate({ body: { email, name: name || undefined, role, password } });
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field id="user-email" label="Email" error={fields.email}>
        <Input type="email" required maxLength={320} value={email} onChange={(e) => setEmail(e.target.value)} />
      </Field>
      <Field id="user-name" label="Name" error={fields.name}>
        <Input maxLength={200} value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field id="user-role" label="Role" error={fields.role}>
        <Select value={role} onChange={(e) => setRole(e.target.value as Role)}>
          {roles.map((r) => (
            <option key={r} value={r}>
              {roleLabels[r]}
            </option>
          ))}
        </Select>
      </Field>
      <Field
        id="user-password"
        label="Temporary password"
        hint="At least 10 characters. The user must change it at first sign-in."
        error={fields.password}
      >
        <Input
          type="text"
          autoComplete="off"
          required
          minLength={10}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          Create user
        </Button>
      </div>
    </form>
  );
}

function ResetPasswordForm({ user, onDone }: { user: User; onDone: () => void }) {
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const mutation = useMutation({
    ...resetUserPasswordMutation(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: usersQueryKey });
      toast({ title: "Password reset", description: `${user.email} must change it at the next sign-in.` });
      onDone();
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    mutation.mutate({ path: { userId: user.id }, body: { password } });
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <p className="text-sm">
        Set a temporary password for <span className="font-medium">{user.email}</span>. The user must change it at the
        next sign-in.
      </p>
      <Field id="reset-password" label="Temporary password" error={fieldErrors(mutation.error).password}>
        <Input
          type="text"
          autoComplete="off"
          required
          minLength={10}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </Field>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          Reset password
        </Button>
      </div>
    </form>
  );
}
