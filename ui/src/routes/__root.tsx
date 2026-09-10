import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Outlet, useNavigate, useRouter, useRouterState } from "@tanstack/react-router";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { ApiError, onUnauthorized } from "@/api/client";
import { AppShell } from "@/components/app-shell";
import { ChangePasswordForm } from "@/components/change-password-form";
import { Button } from "@/components/ui/button";
import { meQueryKey, useMe } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: Root,
});

function FullScreen({ children }: { children: ReactNode }) {
  return <div className="flex min-h-screen items-center justify-center p-4">{children}</div>;
}

function Root() {
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
      <FullScreen>
        <div role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
          Loading
        </div>
      </FullScreen>
    );
  }

  if (me.isError) {
    return (
      <FullScreen>
        <div role="alert" className="flex items-center gap-2 text-sm text-destructive">
          <AlertTriangle className="h-4 w-4" aria-hidden />
          {errorMessage(me.error)}
          <button type="button" className="underline" onClick={() => void me.refetch()}>
            Retry
          </button>
        </div>
      </FullScreen>
    );
  }

  if (me.data.must_change_password) {
    return (
      <FullScreen>
        <div className="flex w-full max-w-sm flex-col gap-4 rounded-[8px] border bg-panel p-6">
          <div className="flex flex-col gap-1">
            <h1 className="text-lg font-semibold">Set a new password</h1>
            <p className="text-sm text-muted-foreground">
              Your password is temporary. Set a new password to continue as {me.data.email}.
            </p>
          </div>
          <ChangePasswordForm
            submitLabel="Set password"
            onSuccess={() => void qc.invalidateQueries({ queryKey: meQueryKey })}
          />
          <Button
            variant="ghost"
            onClick={() => {
              qc.clear();
              void navigate({ to: "/login" });
            }}
          >
            Use another account
          </Button>
        </div>
      </FullScreen>
    );
  }

  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}
