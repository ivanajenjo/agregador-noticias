import { Request, Response } from 'express';

//Modelos
import Entrada, { IEntrada } from '../models/entrada';

export async function getEntradas(req: Request, res: Response): Promise<Response> {
    const entradas = await Entrada.find();
    return res.json(entradas);
};