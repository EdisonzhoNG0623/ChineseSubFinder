import BaseApi from './BaseApi';

class AIApi extends BaseApi {
  getStatus = () => this.http('/v1/ai/status');

  test = () => this.http('/v1/ai/test', {}, 'POST');
}

export default new AIApi();
