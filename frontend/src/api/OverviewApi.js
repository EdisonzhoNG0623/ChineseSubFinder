import BaseApi from './BaseApi';

class OverviewApi extends BaseApi {
  get = (days = 7) => this.http('/v1/overview', { days });
}

export default new OverviewApi();
