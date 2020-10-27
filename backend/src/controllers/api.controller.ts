import {Request, Response} from 'express'
import config from '../config/config'

export const home = (req: Request, res: Response) => {
    return res.send('Home');
}