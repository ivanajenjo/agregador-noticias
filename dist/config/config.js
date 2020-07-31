"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.default = {
    jwtSecret: process.env.JSW_SECRET || 'secrettoken',
    DB: {
        URI: process.env.MONGODB_URI || 'mongodb://localhost/agregador-noticias',
        USER: process.env.MONGODB_USER,
        PASSWORD: process.env.MONGODB_PASSWORD
    }
};
