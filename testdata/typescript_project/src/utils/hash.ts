import { randomBytes, scrypt, timingSafeEqual } from 'crypto';
import { promisify } from 'util';

const scryptAsync = promisify(scrypt);

const KEY_LENGTH = 64;
const DEFAULT_SALT_LENGTH = 16;

/**
 * Generate a cryptographically secure random salt.
 *
 * @param length - Number of random bytes (hex-encoded output is twice this).
 * @returns Hex-encoded salt string.
 */
export function generateSalt(length: number = DEFAULT_SALT_LENGTH): string {
  if (length <= 0) {
    throw new RangeError('Salt length must be positive');
  }
  return randomBytes(length).toString('hex');
}

/**
 * Hash a password using scrypt with a random salt.
 *
 * The returned string has the format `salt:derivedKey` so the salt is
 * preserved alongside the hash for later verification.
 *
 * @param password - The plaintext password.
 * @returns A combined `salt:hash` string.
 */
export async function hashPassword(password: string): Promise<string> {
  const salt = generateSalt();
  const derived = (await scryptAsync(password, salt, KEY_LENGTH)) as Buffer;
  return `${salt}:${derived.toString('hex')}`;
}

/**
 * Compare a plaintext password against a previously hashed value.
 *
 * Uses timing-safe comparison to mitigate timing attacks.
 *
 * @param password - The plaintext password to verify.
 * @param hash - The stored `salt:hash` string from {@link hashPassword}.
 * @returns `true` when the password matches.
 */
export async function comparePassword(
  password: string,
  hash: string,
): Promise<boolean> {
  const [salt, storedKey] = hash.split(':');
  if (!salt || !storedKey) {
    return false;
  }

  const storedBuffer = Buffer.from(storedKey, 'hex');
  const derived = (await scryptAsync(password, salt, KEY_LENGTH)) as Buffer;

  if (storedBuffer.length !== derived.length) {
    return false;
  }

  return timingSafeEqual(storedBuffer, derived);
}
