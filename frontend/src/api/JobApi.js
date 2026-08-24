import BaseApi from './BaseApi';

class JobApi extends BaseApi {
  getStatus = () => this.http('/v1/daemon/status');

  start = (data) => this.http('/v1/daemon/start', data, 'POST');

  stop = (data) => this.http('/v1/daemon/stop', data, 'POST');

  getList = () => this.http('/v1/jobs/list');

  getPage = (params) => this.http('/v1/jobs', params);

  update = (id, data) => this.http(`/v1/jobs/change-job-status`, { id, ...data }, 'POST');

  getLog = (id) => this.http(`/v1/jobs/log`, { id }, 'POST');
}
export default new JobApi();
