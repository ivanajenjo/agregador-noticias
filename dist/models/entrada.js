"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const mongoose_1 = require("mongoose");
const entradaSchema = new mongoose_1.Schema({
    nombre: {
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
    imagePath: String,
});
exports.default = mongoose_1.model('Entrada', entradaSchema);
