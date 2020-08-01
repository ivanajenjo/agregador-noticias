"use strict";
var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.crearEntrada = exports.getEntradas = void 0;
//Modelos
const entrada_1 = __importDefault(require("../models/entrada"));
function getEntradas(req, res) {
    return __awaiter(this, void 0, void 0, function* () {
        const entradas = yield entrada_1.default.find();
        return res.json(entradas);
    });
}
exports.getEntradas = getEntradas;
;
function crearEntrada(req, res) {
    return __awaiter(this, void 0, void 0, function* () {
        const { title, description } = req.body;
        const newEntrada = { title, description, imagePath: req.file.path };
        const photo = new entrada_1.default(newEntrada);
        yield photo.save();
        return res.json({
            message: 'Photo Saved Successfully',
            photo
        });
    });
}
exports.crearEntrada = crearEntrada;
;
