import express from "express";
import morgan from "morgan";

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

export default app