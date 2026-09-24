import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, CheckCircle2, Clock, Copy, KeyRound, Plus, TriangleAlert } from "lucide-react";
import { useState, type FormEvent } from "react";
import { createTokenMutation, listTokensInfiniteOptions, revokeTokenMutation } from "@/api/@tanstack/react-query.gen";
import type { CreatedToken, Token } from "@/api/types.gen";
import { DataState } from "@/components/data-state";
import { LoadMore } from "@/components/load-more";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { SegmentedControl } from "@/components/ui/segmented";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { toast } from "@/components/ui/toast";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { can, roleLabels, rolesUpTo, type Role } from "@/lib/roles";
import { formatTime } from "@/lib/utils";

const tokensQueryKey = [{ _id: "listTokens" }];

function tokenState(t: Token) {
  if (t.revoked_at)
    return (
      <Badge tone="failed" icon={Ban}>
        Revoked
      </Badge>
    );
  if (t.expires_at && new Date(t.expires_at).getTime() < Date.now()) {
    return (
      <Badge tone="warning" icon={Clock}>
        Expired
      </Badge>
    );
  }
  return (
    <Badge tone="success" icon={CheckCircle2}>
      Active
    </Badge>
  );
}

export function TokensPage() {
  const me = useCurrentUser();
  const isAdmin = can(me.role, "admin");
  const [all, setAll] = useState(false);
  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<Token | null>(null);
  const showAll = isAdmin && all;

  const tokens = useInfiniteQuery({
    ...listTokensInfiniteOptions({ query: { all: showAll || undefined } }),
    initialPageParam: {},
    getNextPageParam: (last) => (last.next_cursor ? { query: { cursor: last.next_cursor } } : undefined),
    select: (d) => d.pages.flatMap((p) => p.items),
  });

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="API tokens"
        description="Tokens let scripts and tools call the API with a role up to your own."
        actions={
          <Button onClick={() => setCreating(true)}>
            <Plus className="h-4 w-4" aria-hidden />
            Create token
          </Button>
        }
      />
      {isAdmin && (
        <SegmentedControl
          label="Owner"
          size="sm"
          options={[
            { value: "mine", label: "My tokens" },
            { value: "all", label: "All users" },
          ]}
          value={all ? "all" : "mine"}
          onChange={(v) => setAll(v === "all")}
        />
      )}
      <DataState query={tokens} empty={(d) => d.length === 0} emptyText="No API tokens exist." emptyIcon={KeyRound}>
        {(items) => (
          <>
            <Table>
              <THead>
                <Tr>
                  <Th>Name</Th>
                  {showAll && <Th>Owner</Th>}
                  <Th>Prefix</Th>
                  <Th>Role</Th>
                  <Th>Created</Th>
                  <Th>Expires</Th>
                  <Th>Last used</Th>
                  <Th>State</Th>
                  <Th className="relative">
                    <span className="sr-only">Actions</span>
                  </Th>
                </Tr>
              </THead>
              <TBody>
                {items.map((t) => (
                  <Tr key={t.id}>
                    <Td className="font-medium">{t.name}</Td>
                    {showAll && <Td>{t.user_email}</Td>}
                    <Td className="font-mono text-xs text-muted-foreground">{t.prefix}</Td>
                    <Td>{roleLabels[t.role]}</Td>
                    <Td className="text-muted-foreground tabular-nums">{formatTime(t.created_at)}</Td>
                    <Td className="text-muted-foreground tabular-nums">
                      {t.expires_at ? formatTime(t.expires_at) : "Never"}
                    </Td>
                    <Td className="text-muted-foreground tabular-nums">
                      {t.last_used_at ? formatTime(t.last_used_at) : "Never"}
                    </Td>
                    <Td>{tokenState(t)}</Td>
                    <Td className="text-right">
                      {!t.revoked_at && (
                        <Button variant="ghost" size="sm" onClick={() => setRevoking(t)}>
                          Revoke
                        </Button>
                      )}
                    </Td>
                  </Tr>
                ))}
              </TBody>
            </Table>
            <LoadMore query={tokens} />
          </>
        )}
      </DataState>
      <Dialog open={creating} onClose={() => setCreating(false)} title="Create token">
        <CreateTokenForm role={me.role} onDone={() => setCreating(false)} />
      </Dialog>
      <Dialog open={revoking !== null} onClose={() => setRevoking(null)} title="Revoke token">
        {revoking && <RevokeConfirm token={revoking} onDone={() => setRevoking(null)} />}
      </Dialog>
    </div>
  );
}

function CreateTokenForm({ role, onDone }: { role: Role; onDone: () => void }) {
  const qc = useQueryClient();
  const allowed = rolesUpTo(role);
  const [name, setName] = useState("");
  const [tokenRole, setTokenRole] = useState<Role>(allowed[0] ?? "viewer");
  const [days, setDays] = useState("");
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [copied, setCopied] = useState(false);

  const mutation = useMutation({
    ...createTokenMutation(),
    onSuccess: (data) => {
      setCreated(data);
      void qc.invalidateQueries({ queryKey: tokensQueryKey });
    },
  });

  if (created) {
    const copy = async () => {
      try {
        await navigator.clipboard.writeText(created.secret);
        setCopied(true);
        toast({ title: "Copied", description: "The token is on the clipboard." });
      } catch {
        setCopied(false);
        toast({ title: "Copy failed", description: "Select the token and copy it by hand.", tone: "error" });
      }
    };
    return (
      <div className="flex flex-col gap-4">
        <div className="flex items-start gap-2.5 rounded-control bg-muted px-3 py-2.5 text-sm">
          <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-state-timed-out" aria-hidden />
          <p>Copy this token now. It is not shown again.</p>
        </div>
        <div className="flex gap-2">
          <Input
            aria-label="Token secret"
            readOnly
            value={created.secret}
            className="font-mono text-xs"
            onFocus={(e) => e.currentTarget.select()}
          />
          <Button variant="secondary" onClick={() => void copy()}>
            {copied ? (
              <Check className="h-4 w-4 text-state-success" aria-hidden />
            ) : (
              <Copy className="h-4 w-4" aria-hidden />
            )}
            {copied ? "Copied" : "Copy"}
          </Button>
        </div>
        <div className="flex justify-end">
          <Button onClick={onDone}>Done</Button>
        </div>
      </div>
    );
  }

  const fields = fieldErrors(mutation.error);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    mutation.mutate({ body: { name, role: tokenRole, expires_in_days: days === "" ? undefined : Number(days) } });
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field id="token-name" label="Name" error={fields.name}>
        <Input required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field id="token-role" label="Role" error={fields.role}>
        <Select value={tokenRole} onChange={(e) => setTokenRole(e.target.value as Role)}>
          {allowed.map((r) => (
            <option key={r} value={r}>
              {roleLabels[r]}
            </option>
          ))}
        </Select>
      </Field>
      <Field
        id="token-days"
        label="Expiry in days"
        hint="Optional. From 1 to 365. Leave empty for no expiry."
        error={fields.expires_in_days}
      >
        <Input type="number" min={1} max={365} step={1} value={days} onChange={(e) => setDays(e.target.value)} />
      </Field>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          Create token
        </Button>
      </div>
    </form>
  );
}

function RevokeConfirm({ token, onDone }: { token: Token; onDone: () => void }) {
  const qc = useQueryClient();
  const mutation = useMutation({
    ...revokeTokenMutation(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: tokensQueryKey });
      toast({ title: "Token revoked", description: token.name });
      onDone();
    },
  });
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">
        Revoke the token <span className="font-medium">{token.name}</span>? Clients that use it stop working at once.
      </p>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button
          variant="destructive"
          disabled={mutation.isPending}
          onClick={() => mutation.mutate({ path: { tokenId: token.id } })}
        >
          Revoke
        </Button>
      </div>
    </div>
  );
}
