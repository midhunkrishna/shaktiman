import { EventEmitter } from 'events';
import { createLogger } from './logger.js';

export class Server extends EventEmitter {
    constructor(port, config) {
        super();
        this.port = port;
        this.config = config;
        this.logger = createLogger('server');
    }

    start() {
        this.logger.info(`Starting server on port ${this.port}`);
        this.emit('start', { port: this.port });
    }

    stop() {
        this.logger.info('Stopping server');
        this.emit('stop');
    }

    handleRequest(req, res) {
        this.logger.info(`${req.method} ${req.url}`);
        res.writeHead(200);
        res.end('OK');
    }
}

export function createServer(port) {
    return new Server(port, { timeout: 30000 });
}
