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
    const { title, description } = req.body;
    const newEntrada = { title, description, imagePath: req.file.path };
    const photo = new Entrada(newEntrada);
    await photo.save();
    return res.json({
        message: 'Photo Saved Successfully',
        photo
    });
};