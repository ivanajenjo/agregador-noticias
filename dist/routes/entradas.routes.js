"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const express_1 = require("express");
const router = express_1.Router();
const entrada_controller_1 = require("../controllers/entrada.controller");
router.route('/entradas')
    .get(entrada_controller_1.getEntradas);
exports.default = router;
