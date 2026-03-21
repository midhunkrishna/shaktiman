import { createHmac } from 'crypto';
import { UserRole } from '../models/user';

/** Decoded JWT token payload. */
export interface TokenPayload {
  sub: string;
  email: string;
  role: UserRole;
  iat: number;
  exp: number;
}

const TOKEN_SECRET = process.env.TOKEN_SECRET ?? 'dev-secret';
const TOKEN_TTL_MS = 60 * 60 * 1000; // 1 hour

/**
 * Validate a bearer token by checking its HMAC signature and expiry.
 *
 * @param token - The raw JWT string.
 * @returns `true` when the token is structurally valid and not expired.
 */
export function validateToken(token: string): boolean {
  const parts = token.split('.');
  if (parts.length !== 3) {
    return false;
  }

  const [header, payload, signature] = parts;
  const expected = createHmac('sha256', TOKEN_SECRET)
    .update(`${header}.${payload}`)
    .digest('base64url');

  if (signature !== expected) {
    return false;
  }

  try {
    const decoded: TokenPayload = JSON.parse(
      Buffer.from(payload, 'base64url').toString(),
    );
    return decoded.exp > Date.now();
  } catch {
    return false;
  }
}

/**
 * Exchange an existing token for a fresh one with an extended expiry.
 *
 * @param token - A currently-valid JWT string.
 * @returns A new JWT string with a refreshed `exp` claim.
 * @throws If the provided token is invalid.
 */
export async function refreshToken(token: string): Promise<string> {
  if (!validateToken(token)) {
    throw new Error('Cannot refresh an invalid token');
  }

  const payload = token.split('.')[1];
  const decoded: TokenPayload = JSON.parse(
    Buffer.from(payload, 'base64url').toString(),
  );

  const refreshed: TokenPayload = {
    ...decoded,
    iat: Date.now(),
    exp: Date.now() + TOKEN_TTL_MS,
  };

  const header = Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url');
  const body = Buffer.from(JSON.stringify(refreshed)).toString('base64url');
  const signature = createHmac('sha256', TOKEN_SECRET)
    .update(`${header}.${body}`)
    .digest('base64url');

  return `${header}.${body}.${signature}`;
}
