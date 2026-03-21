import { Request, Response, NextFunction } from 'express';
import { validateToken } from './login';

/** Configuration for the authentication middleware. */
export interface AuthConfig {
  /** HTTP paths that skip authentication. */
  publicPaths: string[];
  /** Header name carrying the bearer token. */
  headerName: string;
  /** Whether to attach the decoded payload to `req`. */
  attachPayload: boolean;
}

/** Express-compatible middleware function. */
export type Middleware = (req: Request, res: Response, next: NextFunction) => void;

const DEFAULT_CONFIG: AuthConfig = {
  publicPaths: ['/health', '/readiness'],
  headerName: 'authorization',
  attachPayload: true,
};

/**
 * Standalone auth guard that rejects unauthenticated requests with 401.
 *
 * Uses the default configuration. For customisation, prefer
 * {@link createAuthMiddleware}.
 */
export function authGuard(req: Request, res: Response, next: NextFunction): void {
  const header = req.headers['authorization'];
  if (!header || !header.startsWith('Bearer ')) {
    res.status(401).json({ error: 'Missing or malformed Authorization header' });
    return;
  }

  const token = header.slice(7);
  if (!validateToken(token)) {
    res.status(401).json({ error: 'Invalid or expired token' });
    return;
  }

  next();
}

/**
 * Create a configurable authentication middleware.
 *
 * @param config - Partial configuration merged with sensible defaults.
 * @returns An Express middleware function.
 */
export function createAuthMiddleware(config: Partial<AuthConfig> = {}): Middleware {
  const merged: AuthConfig = { ...DEFAULT_CONFIG, ...config };

  return (req: Request, res: Response, next: NextFunction): void => {
    if (merged.publicPaths.includes(req.path)) {
      next();
      return;
    }

    const header = req.headers[merged.headerName.toLowerCase()] as string | undefined;
    if (!header || !header.startsWith('Bearer ')) {
      res.status(401).json({ error: 'Unauthorized' });
      return;
    }

    const token = header.slice(7);
    if (!validateToken(token)) {
      res.status(401).json({ error: 'Token validation failed' });
      return;
    }

    next();
  };
}
