import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { useEffect, useState, type FormEvent } from "react";
import { loginMutation } from "@/api/@tanstack/react-query.gen";
import { ApiError } from "@/api-client";
import { Button } from "@/components/ui/button";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { AuthSheet } from "@/features/auth/auth-sheet";
import { meQueryKey, useMe } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";

/** safeRedirect accepts only same-origin paths. */
function safeRedirect(r: string | undefined): string {
  if (!r || !r.startsWith("/") || r.startsWith("//") || r.startsWith("/login")) return "/";
  return r;
}

export function LoginPage({ redirect }: { redirect?: string }) {
  const router = useRouter();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  useEffect(() => {
    if (me.data) void navigate({ to: "/", replace: true });
  }, [me.data, navigate]);

  const login = useMutation({
    ...loginMutation(),
    onSuccess: (data) => {
      qc.clear();
      qc.setQueryData(meQueryKey(), data);
      router.history.replace(safeRedirect(redirect));
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate({ body: { email, password } });
  };

  let error = "";
  if (login.isError) {
    const err = login.error;
    if (err instanceof ApiError && err.status === 429) error = "Too many attempts. Wait and try again.";
    else if (err instanceof ApiError && err.status === 401) error = err.message || "The email or password is wrong.";
    else error = errorMessage(err);
  }

  return (
    <AuthSheet title="Sign in to Sluice" description="Use the email and password of your Sluice account.">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <Field id="login-email" label="Email">
          <Input
            type="email"
            autoComplete="username"
            required
            autoFocus
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <Field id="login-password" label="Password">
          <Input
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <FormError>{error}</FormError>
        <Button type="submit" className="mt-1 w-full" disabled={login.isPending}>
          {login.isPending ? "Signing in" : "Sign in"}
        </Button>
      </form>
    </AuthSheet>
  );
}
