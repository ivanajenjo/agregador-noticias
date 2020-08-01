import express from "express";
import morgan from "morgan";

import apiroutes from './routes/api.routes';
import entradasroutes from './routes/entradas.routes';

//inicializaciones
const app = express();

//configuraciones
app.set('port', process.env.PORT || 3000);

//middlewares
app.use(morgan('dev'));

//rutas
app.get('/', (req, res) => {
    res.send('Hola Mundo');
});
app.use(apiroutes);
app.use(entradasroutes);

export default app