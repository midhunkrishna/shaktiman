export class Logger {
    constructor(name) {
        this.name = name;
    }

    info(message) {
        console.log(`[${this.name}] INFO: ${message}`);
    }

    error(message) {
        console.error(`[${this.name}] ERROR: ${message}`);
    }

    warn(message) {
        console.warn(`[${this.name}] WARN: ${message}`);
    }
}

export function createLogger(name) {
    return new Logger(name);
}
