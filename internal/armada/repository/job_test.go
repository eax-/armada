package repository

import (
	"testing"
	"time"

	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/G-Research/armada/internal/armada/authorization"
	"github.com/G-Research/armada/internal/common"
	"github.com/G-Research/armada/pkg/api"
)

func TestJobCanBeLeasedOnlyOnce(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {

		job := addLeasedJob(t, r, "queue1", "cluster1")

		leasedAgain, e := r.TryLeaseJobs("cluster2", "queue1", []*api.Job{job})
		assert.Nil(t, e)
		assert.Equal(t, 0, len(leasedAgain))
	})
}

func TestJobLeaseCanBeRenewed(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addLeasedJob(t, r, "queue1", "cluster1")

		renewed, e := r.RenewLease("cluster1", []string{job.Id})
		assert.Nil(t, e)
		assert.Equal(t, 1, len(renewed))
		assert.Equal(t, job.Id, renewed[0])
	})
}

func TestJobLeaseExpiry(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addLeasedJob(t, r, "queue1", "cluster1")
		deadline := time.Now()
		addLeasedJob(t, r, "queue1", "cluster1")

		_, e := r.ExpireLeases("queue1", deadline)
		assert.Nil(t, e)

		queued, e := r.PeekQueue("queue1", 10)
		assert.Nil(t, e)
		assert.Equal(t, 1, len(queued), "Queue should have one job which expired")
		assert.Equal(t, job.Id, queued[0].Id)
	})
}

func TestEvenExpiredLeaseCanBeRenewed(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addLeasedJob(t, r, "queue1", "cluster1")
		deadline := time.Now()

		_, e := r.ExpireLeases("queue1", deadline)
		assert.Nil(t, e)

		renewed, e := r.RenewLease("cluster1", []string{job.Id})
		assert.Nil(t, e)
		assert.Equal(t, 1, len(renewed))
		assert.Equal(t, job.Id, renewed[0])
	})
}

func TestRenewingLeaseFailsForJobAssignedToDifferentCluster(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addLeasedJob(t, r, "queue1", "cluster1")

		renewed, e := r.RenewLease("cluster2", []string{job.Id})
		assert.Nil(t, e)
		assert.Equal(t, 0, len(renewed))
	})
}

func TestRenewingNonExistentLease(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		renewed, e := r.RenewLease("cluster2", []string{"missingJobId"})
		assert.Nil(t, e)
		assert.Equal(t, 0, len(renewed))
	})
}

func TestDeletingExpiredJobShouldDeleteJobFromQueue(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addLeasedJob(t, r, "queue1", "cluster1")
		deadline := time.Now()

		_, e := r.ExpireLeases("queue1", deadline)
		assert.Nil(t, e)

		deletionResult := r.DeleteJobs([]*api.Job{job})

		err, deleted := deletionResult[job]

		assert.Equal(t, 1, len(deletionResult))
		assert.True(t, deleted)
		assert.Nil(t, err)

		queue, e := r.PeekQueue("queue1", 100)
		assert.Nil(t, e)
		assert.Equal(t, 0, len(queue))
	})
}

func TestReturnLeaseShouldReturnJobToQueue(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addLeasedJob(t, r, "queue1", "cluster1")

		returned, e := r.ReturnLease("cluster1", job.Id)
		assert.Nil(t, e)
		assert.NotNil(t, returned)

		queue, e := r.PeekQueue("queue1", 100)
		assert.Nil(t, e)
		assert.Equal(t, 1, len(queue))
		assert.Equal(t, job.Id, returned.Id)
	})
}

func TestReturnLeaseFromDifferentClusterIsNoop(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addLeasedJob(t, r, "queue1", "cluster1")

		returned, e := r.ReturnLease("cluster2", job.Id)
		assert.Nil(t, e)
		assert.Nil(t, returned)

		queue, e := r.PeekQueue("queue1", 100)
		assert.Nil(t, e)
		assert.Equal(t, 0, len(queue))
	})
}

func TestReturnLeaseForJobInQueueIsNoop(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addTestJob(t, r, "queue1")

		returned, e := r.ReturnLease("cluster2", job.Id)
		assert.Nil(t, e)
		assert.Nil(t, returned)
	})
}

func TestDeleteRunningJob(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addLeasedJob(t, r, "queue1", "cluster1")

		result := r.DeleteJobs([]*api.Job{job})
		err, deletionOccurred := result[job]
		assert.Nil(t, err)
		assert.True(t, deletionOccurred)
	})
}

func TestDeleteQueuedJob(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		job := addTestJob(t, r, "queue1")

		result := r.DeleteJobs([]*api.Job{job})
		err, deletionOccurred := result[job]
		assert.Nil(t, err)
		assert.True(t, deletionOccurred)
	})
}

func TestDeleteWithSomeMissingJobs(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		missingJob := &api.Job{Id: "jobId"}
		runningJob := addLeasedJob(t, r, "queue1", "cluster1")
		result := r.DeleteJobs([]*api.Job{missingJob, runningJob})

		err, deletionOccurred := result[missingJob]
		assert.Nil(t, err)
		assert.False(t, deletionOccurred)

		err, deletionOccurred = result[runningJob]
		assert.Nil(t, err)
		assert.True(t, deletionOccurred)
	})
}

func TestReturnLeaseForDeletedJobShouldKeepJobDeleted(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {

		job := addLeasedJob(t, r, "cancel-test-queue", "cluster")

		result := r.DeleteJobs([]*api.Job{job})
		assert.Nil(t, result[job])

		returned, err := r.ReturnLease("cluster", job.Id)
		assert.Nil(t, returned)
		assert.Nil(t, err)

		q, err := r.PeekQueue("cancel-test-queue", 100)
		assert.Nil(t, err)
		assert.Empty(t, q)
	})
}

func TestGetActiveJobIds(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		addTestJob(t, r, "queue1")
		addLeasedJob(t, r, "queue1", "cluster1")
		addTestJob(t, r, "queue2")

		ids, e := r.GetActiveJobIds("queue1", "set1")
		assert.Nil(t, e)
		assert.Equal(t, 2, len(ids))
	})
}

func TestGetQueueActiveJobSets(t *testing.T) {
	withRepository(func(r *RedisJobRepository) {
		addTestJob(t, r, "queue1")
		addLeasedJob(t, r, "queue1", "cluster1")
		addTestJob(t, r, "queue2")

		infos, e := r.GetQueueActiveJobSets("queue1")
		assert.Nil(t, e)
		assert.Equal(t, []*api.JobSetInfo{{
			Name:       "set1",
			QueuedJobs: 1,
			LeasedJobs: 1,
		}}, infos)
	})
}

func TestCreateJob_ApplyDefaultLimitss(t *testing.T) {
	defaults := common.ComputeResources{
		"cpu":               resource.MustParse("1"),
		"memory":            resource.MustParse("512Mi"),
		"ephemeral-storage": resource.MustParse("4Gi")}

	withRepositoryUsingJobDefaults(defaults, func(r *RedisJobRepository) {
		testCases := map[*v1.ResourceList]v1.ResourceList{
			nil: {
				"cpu":               resource.MustParse("1"),
				"memory":            resource.MustParse("512Mi"),
				"ephemeral-storage": resource.MustParse("4Gi"),
			},
			{
				"cpu": resource.MustParse("2"),
			}: {
				"cpu":               resource.MustParse("2"),
				"memory":            resource.MustParse("512Mi"),
				"ephemeral-storage": resource.MustParse("4Gi"),
			},
			{
				"nvidia/gpu": resource.MustParse("3"),
			}: {
				"cpu":               resource.MustParse("1"),
				"memory":            resource.MustParse("512Mi"),
				"ephemeral-storage": resource.MustParse("4Gi"),
				"nvidia/gpu":        resource.MustParse("3"),
			},
		}

		for requirements, expected := range testCases {
			resources := v1.ResourceRequirements{}
			if requirements != nil {
				resources.Requests = *requirements
				resources.Limits = *requirements
			}
			job := addTestJobWithRequirements(t, r, "test", resources)
			assert.Equal(t, expected, job.PodSpec.Containers[0].Resources.Limits)
			assert.Equal(t, expected, job.PodSpec.Containers[0].Resources.Requests)
		}
	})
}

func addLeasedJob(t *testing.T, r *RedisJobRepository, queue string, cluster string) *api.Job {
	job := addTestJob(t, r, queue)
	leased, e := r.TryLeaseJobs(cluster, queue, []*api.Job{job})
	assert.Nil(t, e)
	assert.Equal(t, 1, len(leased))
	assert.Equal(t, job.Id, leased[0].Id)
	return job
}

func addTestJob(t *testing.T, r *RedisJobRepository, queue string) *api.Job {
	cpu := resource.MustParse("1")
	memory := resource.MustParse("512Mi")

	return addTestJobWithRequirements(t, r, queue, v1.ResourceRequirements{
		Limits:   v1.ResourceList{"cpu": cpu, "memory": memory},
		Requests: v1.ResourceList{"cpu": cpu, "memory": memory},
	})
}

func addTestJobWithRequirements(t *testing.T, r *RedisJobRepository, queue string, requirements v1.ResourceRequirements) *api.Job {

	jobs, e := r.CreateJobs(&api.JobSubmitRequest{
		Queue:    queue,
		JobSetId: "set1",
		JobRequestItems: []*api.JobSubmitRequestItem{
			{
				Priority: 1,
				PodSpec: &v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: requirements,
						},
					},
				},
			},
		},
	}, authorization.NewStaticPrincipal("user", []string{}))
	assert.NoError(t, e)

	results, e := r.AddJobs(jobs)
	assert.Nil(t, e)
	for _, result := range results {
		assert.Empty(t, result.Error)
	}
	return jobs[0]
}

func withRepository(action func(r *RedisJobRepository)) {
	withRepositoryUsingJobDefaults(nil, action)
}

func withRepositoryUsingJobDefaults(jobDefaultLimit common.ComputeResources, action func(r *RedisJobRepository)) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 10})
	defer client.FlushDB()
	defer client.Close()

	client.FlushDB()

	repo := NewRedisJobRepository(client, jobDefaultLimit)
	action(repo)
}
