"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const express_1 = require("express");
const router = express_1.Router();
const api_controller_1 = require("../controllers/api.controller");
router.get('/home', api_controller_1.home);
exports.default = router;
