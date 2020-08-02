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
const express_1 = __importDefault(require("express"));
const morgan_1 = __importDefault(require("morgan"));
const path_1 = __importDefault(require("path"));
const scraper_1 = require("./libs/scraper");
const node_cron_1 = __importDefault(require("node-cron"));
const api_routes_1 = __importDefault(require("./routes/api.routes"));
const entradas_routes_1 = __importDefault(require("./routes/entradas.routes"));
//inicializaciones
const app = express_1.default();
//configuraciones
app.set('port', process.env.PORT || 3000);
//middlewares
app.use(morgan_1.default('dev'));
app.use(express_1.default.json());
//Tareas Programadas Por horas
node_cron_1.default.schedule("* 9,15,22 * * *", function () {
    return __awaiter(this, void 0, void 0, function* () {
        console.log("Realizando capturas");
        yield scraper_1.getCapturas();
    });
});
//rutas
app.get('/', (req, res) => {
    res.send('Hola Mundo');
});
app.use('/capturas', express_1.default.static(path_1.default.resolve('capturas')));
app.use('/home', api_routes_1.default);
app.use('/entradas', entradas_routes_1.default);
exports.default = app;
