"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const express_1 = require("express");
const router = express_1.Router();
const multer_1 = __importDefault(require("../libs/multer"));
const entrada_controller_1 = require("../controllers/entrada.controller");
router.route('/entradas')
    .get(entrada_controller_1.getEntradas)
    .post(multer_1.default.single('image'), entrada_controller_1.crearEntrada);
exports.default = router;
