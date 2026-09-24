import { useQueryClient } from "@tanstack/react-query";
import { Outlet, useNavigate, useRouter, useRouterState } from "@tanstack/react-router";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useEffect } from "react";
import { ApiError, onUnauthorized } from "@/api-client";
import { AppShell } from "@/app/app-shell";
import { AuthSheet, BrandMark } from "@/features/auth/auth-sheet";
import { ChangePasswordForm } from "@/features/auth/change-password-form";
import { Button } from "@/components/ui/button";
import { meQueryKey, useMe } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";

export function RootShell() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const router = useRouter();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const isLogin = pathname === "/login";

  useEffect(() => {
    const off = onUnauthorized(() => {
      const loc = router.state.location;
      if (loc.pathname === "/login") return;
      qc.clear();
      void navigate({ to: "/login", search: { redirect: loc.href } });
    });
    return () => {
      off();
    };
  }, [router, navigate, qc]);

  const unauthorized = me.error instanceof ApiError && me.error.status === 401;
  useEffect(() => {
    if (!isLogin && unauthorized) {
      void navigate({ to: "/login", search: { redirect: router.state.location.href } });
    }
  }, [isLogin, unauthorized, navigate, router]);

  if (isLogin) return <Outlet />;

  if (me.isPending || unauthorized) {
    return (
      <div className="window-backdrop flex min-h-screen items-center justify-center p-4">
        <div role="status" className="flex animate-enter flex-col items-center gap-4">
          <BrandMark />
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-hidden />
          <span className="sr-only">Loading</span>
        </div>
      </div>
    );
  }

  if (me.isError) {
    return (
      <AuthSheet>
        <div role="alert" className="flex flex-col items-center gap-3 text-center">
          <span className="flex h-11 w-11 items-center justify-center rounded-full bg-destructive/10 text-destructive">
            <AlertTriangle className="h-5 w-5" aria-hidden />
          </span>
          <div className="flex flex-col gap-1">
            <h1 className="text-lg font-semibold tracking-tight">Your account did not load</h1>
            <p className="text-sm break-words text-muted-foreground">{errorMessage(me.error)}</p>
          </div>
        </div>
        <Button className="w-full" onClick={() => void me.refetch()} disabled={me.isFetching}>
          {me.isFetching ? "Retrying" : "Retry"}
        </Button>
      </AuthSheet>
    );
  }

  if (me.data.must_change_password) {
    return (
      <AuthSheet
        title="Set a new password"
        description={`Your password is temporary. Set a new password to continue as ${me.data.email}.`}
      >
        <ChangePasswordForm
          submitLabel="Set password"
          layout="sheet"
          onSuccess={() => void qc.invalidateQueries({ queryKey: meQueryKey() })}
        />
        <Button
          variant="ghost"
          className="-mt-2 w-full"
          onClick={() => {
            qc.clear();
            void navigate({ to: "/login" });
          }}
        >
          Use another account
        </Button>
      </AuthSheet>
    );
  }

  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}
