const commitPaths: Record<string, string> = {
  "github.com": "/commit/",
  "gitlab.com": "/-/commit/",
  "bitbucket.org": "/commits/",
};

/**
 * commitUrl returns the web page of a commit on github.com, gitlab.com or bitbucket.org,
 * from an https, ssh:// or scp-like repository URL. Other hosts give undefined.
 */
export function commitUrl(repoUrl: string, sha: string): string | undefined {
  if (!sha) return undefined;
  const where = repoLocation(repoUrl.trim());
  if (!where) return undefined;
  const { host, path } = where;
  const commit = commitPaths[host.toLowerCase()];
  const repo = path
    .replace(/^\/+/, "")
    .replace(/\/+$/, "")
    .replace(/\.git$/, "");
  if (!commit || !repo.includes("/")) return undefined;
  return `https://${host.toLowerCase()}/${repo}${commit}${sha}`;
}

function repoLocation(repoUrl: string): { host: string; path: string } | undefined {
  const scp = /^[\w.-]+@([\w.-]+):(.+)$/.exec(repoUrl);
  if (scp) return { host: scp[1]!, path: scp[2]! };
  try {
    const u = new URL(repoUrl);
    return { host: u.hostname, path: u.pathname };
  } catch {
    return undefined;
  }
}
