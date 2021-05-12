import {Request, Response} from 'express'

export const home = (req: Request, res: Response) => {
    return res.send('Home');
}