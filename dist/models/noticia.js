"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const mongoose_1 = require("mongoose");
const noticiaSchema = new mongoose_1.Schema({
    titular: {
        type: String,
        required: true,
    },
    fuente: {
        type: String,
        required: true,
    },
    fecha: {
        type: Date,
        required: true,
    },
});
exports.default = mongoose_1.model('Noticia', noticiaSchema);
