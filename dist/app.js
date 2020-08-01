"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const express_1 = __importDefault(require("express"));
const morgan_1 = __importDefault(require("morgan"));
const api_routes_1 = __importDefault(require("./routes/api.routes"));
const entradas_routes_1 = __importDefault(require("./routes/entradas.routes"));
//inicializaciones
const app = express_1.default();
//configuraciones
app.set('port', process.env.PORT || 3000);
//middlewares
app.use(morgan_1.default('dev'));
//rutas
app.get('/', (req, res) => {
    res.send('Hola Mundo');
});
app.use(api_routes_1.default);
app.use(entradas_routes_1.default);
exports.default = app;
