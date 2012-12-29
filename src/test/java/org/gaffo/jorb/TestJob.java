package org.gaffo.jorb;

import java.util.concurrent.atomic.AtomicBoolean;

public class TestJob implements IJob
{
    @Override
    public void process(IContext context)
    {
        AtomicBoolean ab = context.get(AtomicBoolean.class);
        ab.set(true);
    }
}