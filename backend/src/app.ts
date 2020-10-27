import express from "express";
import morgan from "morgan";
import path from "path";
import { getCapturas } from "./libs/scraper";
import cron from "node-cron";

import apiroutes from "./routes/api.routes";
import entradasroutes from "./routes/entradas.routes";

//inicializaciones
const app = express();

//configuraciones
app.set("port", process.env.PORT || 3000);

//middlewares
app.use(morgan("dev"));
app.use(express.json());

//Tareas Programadas Por horas
cron.schedule("* 9,15,22 * * *", async function () {
  console.log("Realizando capturas");
  getCapturas();
});

//rutas
app.get("/", (req, res) => {
  res.send("Hola Mundo");
});

app.get("/realizarCapturas", (req, res) => {
    res.send("Realizando Capturas");
    getCapturas();
    console.log("Capturas Realizadas");
});

app.use("/capturas", express.static(path.resolve("capturas")));
app.use("/home", apiroutes);
app.use("/entradas", entradasroutes);

export default app;
