import { Router } from 'express'
const router = Router();

import upload from '../libs/multer';
import { getEntradas, crearEntrada } from "../controllers/entrada.controller";

router.route('/entradas')
    .get(getEntradas)
    .post(upload.single('image'), crearEntrada);

export default router;