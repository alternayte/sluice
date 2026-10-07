package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/alternayte/sluice/internal/secret"
)

// secretsPath returns the API path of the secrets of a scope: global when ns is empty.
func secretsPath(ns string) string {
	if ns == "" {
		return "/api/v1/secrets"
	}
	return "/api/v1/namespaces/" + url.PathEscape(ns) + "/secrets"
}

// runSecretsList implements `sluice secrets list`.
func runSecretsList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("secrets list", "secrets list [--namespace name]", stdout, stderr)
	ns := c.fs.String("namespace", "", "list the secrets that the namespace sees (default: the global secrets)")
	if _, code := c.parse(args, 0); code >= 0 {
		return code
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	var raw json.RawMessage
	if err := r.do(ctx, http.MethodGet, secretsPath(*ns), nil, &raw); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(raw)
		return exitOK
	}
	var list secret.SecretList
	if err := json.Unmarshal(raw, &list); err != nil {
		return c.fail(err)
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tSCOPE\tPROVIDER\tUPDATED\tLAST USED")
	for _, s := range list.Items {
		used := "—"
		if s.LastResolvedAt != nil {
			used = s.LastResolvedAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Key, s.Scope, s.Provider, s.UpdatedAt.UTC().Format(time.RFC3339), used)
	}
	_ = tw.Flush()
	return exitOK
}

// runSecretsSet implements `sluice secrets set <KEY>`. The value comes from a file or from
// stdin only, so it never shows in the shell history or in the process list.
func runSecretsSet(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("secrets set", "secrets set <KEY> (--from-file path | --stdin) [--namespace name] [--description text]", stdout, stderr)
	ns := c.fs.String("namespace", "", "set a secret of the namespace (default: a global secret)")
	fromFile := c.fs.String("from-file", "", "read the value from this file, as it is")
	stdin := c.fs.Bool("stdin", false, "read the value from stdin, as it is")
	description := c.fs.String("description", "", "description of the secret")
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	if (*fromFile == "") == !*stdin {
		fmt.Fprintln(stderr, "error: give the value with one of --from-file and --stdin")
		return exitConfig
	}
	var b []byte
	var err error
	if *stdin {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(*fromFile)
	}
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "error:", err)
		return exitConfig
	case len(b) == 0:
		fmt.Fprintln(stderr, "error: the value is empty")
		return exitConfig
	case !utf8.Valid(b):
		fmt.Fprintln(stderr, "error: the value is not UTF-8 text; encode a binary value first, for example with base64")
		return exitConfig
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	value := string(b)
	body := secret.SecretPut{Value: &value}
	if *description != "" {
		body.Description = description
	}
	var raw json.RawMessage
	if err := r.do(ctx, http.MethodPut, secretsPath(*ns)+"/"+url.PathEscape(pos[0]), body, &raw); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(raw)
		return exitOK
	}
	var info secret.SecretInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(stdout, "set %s (%s, %d bytes)\n", info.Key, info.Scope, len(b))
	return exitOK
}

// runSecretsCheck implements `sluice secrets check <KEY>`. It exits 1 when the status is not ok.
func runSecretsCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("secrets check", "secrets check <KEY> [--namespace name]", stdout, stderr)
	ns := c.fs.String("namespace", "", "check a secret of the namespace (default: a global secret)")
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	var res secret.CheckResult
	if err := r.do(ctx, http.MethodPost, secretsPath(*ns)+"/"+url.PathEscape(pos[0])+"/check", nil, &res); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(res)
	} else if res.Message != "" {
		fmt.Fprintf(stdout, "%s %s: %s\n", pos[0], res.Status, res.Message)
	} else {
		fmt.Fprintf(stdout, "%s %s\n", pos[0], res.Status)
	}
	if res.Status != "ok" {
		return exitFail
	}
	return exitOK
}

// runSecretsDelete implements `sluice secrets delete <KEY>`.
func runSecretsDelete(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newClientCmd("secrets delete", "secrets delete <KEY> [--namespace name]", stdout, stderr)
	ns := c.fs.String("namespace", "", "delete a secret of the namespace (default: a global secret)")
	pos, code := c.parse(args, 1)
	if code >= 0 {
		return code
	}
	r, code := c.remote()
	if code >= 0 {
		return code
	}
	if err := r.do(ctx, http.MethodDelete, secretsPath(*ns)+"/"+url.PathEscape(pos[0]), nil, nil); err != nil {
		return c.fail(err)
	}
	if c.json() {
		c.printJSON(map[string]any{"key": pos[0], "deleted": true})
	} else {
		fmt.Fprintf(stdout, "deleted %s\n", pos[0])
	}
	return exitOK
}
