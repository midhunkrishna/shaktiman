namespace Validators {
  export interface StringValidator {
    isValid(s: string): boolean;
  }

  export function isEmail(s: string): boolean {
    return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s);
  }

  export class RegexValidator implements StringValidator {
    constructor(private pattern: RegExp) {}

    isValid(s: string): boolean {
      return this.pattern.test(s);
    }
  }
}
