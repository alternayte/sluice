import { PageHeader } from "@/components/ui/page-header";
import { SecretsPanel } from "@/features/secrets/SecretsPanel";

/** SecretsPage shows the global secrets. Namespaces inherit them. */
export function SecretsPage() {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Secrets"
        description="Global secrets. Every namespace inherits them unless the namespace or a parent defines the same key."
      />
      <SecretsPanel />
    </div>
  );
}
