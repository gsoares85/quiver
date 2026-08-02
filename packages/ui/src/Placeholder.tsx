import type { ReactElement } from 'react';

/**
 * Placeholder is a temporary component that proves the shared UI package
 * (`@quiver/ui`) is wired into the desktop frontend. The real design-system
 * components land in later UI tasks.
 */
export function Placeholder(): ReactElement {
  return <div className="quiver-placeholder">Quiver UI — coming soon.</div>;
}
