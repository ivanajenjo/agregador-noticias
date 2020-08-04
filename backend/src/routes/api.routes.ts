import {Router} from 'express'
const router = Router();

import {home} from '../controllers/api.controller'

router.get('/', home);

export default router;