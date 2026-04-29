// Strip git-describe build metadata while preserving branded release suffixes.
// "v2.5.1-3-g4fd653c1" -> "v2.5.1"
// "v3.11.3-otrumb.2" -> "v3.11.3-otrumb.2"
// "dev" -> "dev"
export function cleanVersion(v: string): string {
  return v.replace(/-\d+-g[0-9a-f]+$/i, "");
}
