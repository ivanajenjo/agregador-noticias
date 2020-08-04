import { Request, Response } from 'express';
import fs from 'fs-extra';
import path from 'path'

//Modelos
import Entrada, { IEntrada } from '../models/entrada';

export async function getEntradas(req: Request, res: Response): Promise<Response> {
    const entradas = await Entrada.find();
    return res.json(entradas);
};

export async function crearEntrada(req: Request, res: Response): Promise<Response> {
    const { nombre, fuente } = req.body;
    const fecha = new Date();
    const newEntrada = { nombre, fuente, fecha, imagePath: req.file.path };
    const entrada = new Entrada(newEntrada);
    await entrada.save();
    return res.json({
        message: 'Captura guardada satisfactoriamente',
        entrada
    });
};