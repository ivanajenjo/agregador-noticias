import express from "express";
import morgan from "morgan";

import apiroutes from './routes/api.routes'

//inicializaciones
const app = express();

//configuraciones
app.set('port', process.env.PORT || 3000);

//middlewares
app.use(morgan('dev'));

//rutas
app.get('/', (req, res) => {
    res.send('Hola Mundo')
});
app.use(apiroutes)

export default app