import { Router } from 'express'
const router = Router();

import { getEntradas, crearEntrada } from "../controllers/entrada.controller";

router.route('/entradas')
    .get(getEntradas)
    .post(crearEntrada);

export default router;