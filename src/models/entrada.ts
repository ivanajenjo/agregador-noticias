import mongoose, {model, Schema, Document} from 'mongoose'

export interface IEntrada extends Document{
    nombre: string;
    fuente: string;
    fecha: Date;
    imagePath: String;
}

const entradaSchema = new Schema({
    nombre: {
        type: String,
        required: true,
    },
    fuente:{
        type: String,
        required: true,
    },
    fecha:{
        type: Date,
        required: true,
    },
    imagePath: String,
});

export default model<IEntrada>('Entrada', entradaSchema);