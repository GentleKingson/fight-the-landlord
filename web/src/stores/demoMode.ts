const SUPPORTED_DEMO_MODES = new Set(['table', 'bidding', 'lobby', 'result']);

export function resolveDemoMode(
  search: string,
  enabled = import.meta.env.DEV || import.meta.env.VITE_ENABLE_DEMO === 'true'
): string | null {
  if (!enabled) return null;
  const mode = new URLSearchParams(search).get('demo');
  return mode && SUPPORTED_DEMO_MODES.has(mode) ? mode : null;
}
