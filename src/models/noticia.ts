import mongoose, {model, Schema, Document} from 'mongoose'

export interface INoticia extends Document{
    titular: string;
    fuente: string;
    fecha: Date;
}

const noticiaSchema = new Schema({
    titular: {
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
});

export default model<INoticia>('Noticia', noticiaSchema);