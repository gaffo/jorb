package org.gaffo.jorb;

import org.gaffo.jorb.hornetq.HornetQ;

public interface IQueue {
    void enqueue(IJob job) throws HornetQ.EnqueueException;
}
